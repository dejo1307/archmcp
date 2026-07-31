package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/config"
)

// A snapshot of an unchanged tree must be the same snapshot. That is the property
// the whole baseline diff rests on — a clean diff means something only if a rerun
// over identical inputs is identical.
//
// It used to fail for anyone who set output.dir, for a reason having nothing to do
// with determinism: `.enola/**` was a hard-coded literal in the default ignore list,
// agreeing with Output.Dir only by coincidence. Point output.dir elsewhere and each
// run walked the previous run's artifacts — facts.jsonl, insights.json,
// llm_context.md, plus the previous/ rotation from run 2 onward — so files_seen grew
// every time. Comparability checking cannot catch it either: the config is identical
// on both sides.
func TestSnapshot_CustomOutputDirIsNotIndexedAsSource(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, repo, "go.mod", "module example.com/out\n\ngo 1.25\n")
	writeRepoFile(t, repo, "pkg/x.go", "package pkg\n\nfunc X() string { return \"x\" }\n")

	cfg := config.Default()
	cfg.Repo = repo
	cfg.Output.Dir = ".enola-bench"
	cfg.Explainers = nil

	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var seen []int
	for range 3 {
		snap, err := e.GenerateSnapshot(context.Background(), repo, false)
		if err != nil {
			t.Fatalf("GenerateSnapshot: %v", err)
		}
		// Artifacts have to be WRITTEN for the defect to appear at all: it is the
		// previous run's output on disk that the next walk picks up.
		if err := e.WriteArtifacts(repo); err != nil {
			t.Fatalf("WriteArtifacts: %v", err)
		}
		seen = append(seen, snap.Meta.FilesSeen)
	}

	for i, n := range seen {
		if n != seen[0] {
			t.Errorf("files_seen across three runs of an unchanged tree: %v — run %d walked "+
				"%d files instead of %d, which is the previous run's artifacts being indexed "+
				"as source", seen, i+1, n, seen[0])
			break
		}
	}
}

// The engine must derive the glob even for a config nobody loaded from a file — a
// wrapper, a test, anything assembled in code. Deriving it only in config.Load would
// leave exactly those callers indexing their own output.
func TestNew_DerivesTheOutputDirIgnoreGlob(t *testing.T) {
	cfg := config.Default()
	cfg.Output.Dir = "artifacts/enola"
	if _, err := New(cfg); err != nil {
		t.Fatal(err)
	}

	if !contains(cfg.Ignore, "artifacts/enola/**") {
		t.Errorf("engine.New did not derive an ignore glob for output.dir; ignore = %v", cfg.Ignore)
	}
	// The default location stays ignored too: a repository that used .enola before
	// switching still has artifacts there, and indexing its own history is the same
	// defect wearing a different path.
	if !contains(cfg.Ignore, ".enola/**") {
		t.Errorf("the default .enola/** glob was dropped; ignore = %v", cfg.Ignore)
	}
}

// An absolute output.dir was silently joined to the repository path, producing
// /repo/private/tmp/…/out — a directory nested inside the repo rather than the
// location asked for, and one no derived glob could describe.
func TestNew_RejectsAnUnusableOutputDir(t *testing.T) {
	for _, dir := range []string{filepath.Join(t.TempDir(), "out"), "..", "../elsewhere", "."} {
		cfg := config.Default()
		cfg.Output.Dir = dir
		if _, err := New(cfg); err == nil {
			t.Errorf("output.dir %q was accepted; it cannot be honoured as written", dir)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func writeRepoFile(t *testing.T, repo, rel, content string) {
	t.Helper()
	p := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
