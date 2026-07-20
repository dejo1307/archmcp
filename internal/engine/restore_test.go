package engine_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/explainers/cycles"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
	"github.com/enola-labs/enola/internal/facts"
)

// TestRestoreFromDir verifies a restart-style restore rebuilds the full snapshot
// from disk WITHOUT re-running extractors: facts, insights, and the generated_at
// timestamp all come back. This is the regression against the old facts-only load
// that left Meta with only RepoPath (no generated_at, no insights).
func TestRestoreFromDir(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module testmod\n\ngo 1.21\n")
	// An import cycle so the cycles explainer emits at least one insight to restore.
	writeFile(t, filepath.Join(repo, "pkg", "a", "a.go"), "package a\n\nimport \"testmod/pkg/b\"\n\nfunc A() { b.B() }\n")
	writeFile(t, filepath.Join(repo, "pkg", "b", "b.go"), "package b\n\nimport \"testmod/pkg/a\"\n\nfunc B() { _ = a.A }\n")

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExplainer(cycles.New())

	ctx := context.Background()
	orig, err := eng.GenerateSnapshot(ctx, repo, false)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	if orig.Meta.FactCount == 0 {
		t.Fatal("precondition: expected some facts")
	}
	if len(orig.Insights) == 0 {
		t.Fatal("precondition: expected the cycle to produce an insight")
	}

	// Restore into a BRAND-NEW engine (no extractors registered) — proves nothing
	// is re-extracted, only read from disk.
	fresh, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	label := filepath.Base(repo)
	dir := eng.OutputDir(repo)
	if err := fresh.RestoreFromDir(dir, map[string]string{label: repo}, label); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := fresh.Store().Count(); got != orig.Meta.FactCount {
		t.Errorf("restored fact count = %d, want %d", got, orig.Meta.FactCount)
	}
	snap := fresh.Snapshot()
	if snap == nil {
		t.Fatal("restored snapshot is nil")
	}
	if snap.Meta.GeneratedAt == "" {
		t.Error("restored Meta.GeneratedAt is empty (regression: timestamp not restored)")
	}
	if snap.Meta.GeneratedAt != orig.Meta.GeneratedAt {
		t.Errorf("restored generated_at = %q, want %q", snap.Meta.GeneratedAt, orig.Meta.GeneratedAt)
	}
	if len(snap.Insights) != len(orig.Insights) {
		t.Errorf("restored insights = %d, want %d", len(snap.Insights), len(orig.Insights))
	}
	if snap.Meta.RepoPath == "" {
		t.Error("restored Meta.RepoPath is empty")
	}
}

// TestStaleness_Age checks the age signal in isolation (no git, no global receipt).
// HOME is redirected to an empty temp dir so LoadGlobalReceipt misses and Staleness
// falls back to the in-memory snapshot's generated_at.
func TestStaleness_Age(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	// Older than 24h -> stale.
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{
		GeneratedAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
	}})
	if st := eng.Staleness(24*time.Hour, now); !st.TooOld || !st.Stale() {
		t.Errorf("48h-old snapshot: got TooOld=%v Stale=%v, want both true", st.TooOld, st.Stale())
	}

	// Within 24h -> fresh.
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{
		GeneratedAt: now.Add(-1 * time.Hour).Format(time.RFC3339),
	}})
	if st := eng.Staleness(24*time.Hour, now); st.TooOld || st.Stale() {
		t.Errorf("1h-old snapshot: got TooOld=%v Stale=%v, want both false", st.TooOld, st.Stale())
	}
}

// TestStaleness_CommitMoved checks the VCS-drift signal: a fresh-by-age snapshot
// whose recorded commit no longer matches HEAD is flagged as changed.
func TestStaleness_CommitMoved(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("HOME", t.TempDir())

	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	writeFile(t, filepath.Join(repo, "f.txt"), "hello\n")
	git("add", ".")
	git("commit", "-m", "init")

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{
		RepoPath:    repo,
		GeneratedAt: now.Format(time.RFC3339), // fresh by age
		Git:         &facts.GitInfo{Commit: "0000000000000000000000000000000000000000"},
	}})

	st := eng.Staleness(24*time.Hour, now)
	if st.TooOld {
		t.Errorf("snapshot is fresh by age, TooOld should be false")
	}
	if len(st.Changed) != 1 || st.Changed[0].Reason != "commit moved" {
		t.Errorf("got Changed=%+v, want one 'commit moved'", st.Changed)
	}
	if !st.Stale() {
		t.Error("commit moved should make Stale() true")
	}
}
