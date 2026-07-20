package bootstrap_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/bootstrap"
)

// newEngine builds an engine with defaults (no config file on disk).
func newEngine(t *testing.T) *bootstrap.Engine {
	t.Helper()
	eng, _, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: filepath.Join(t.TempDir(), "no-such-config.yaml"),
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng
}

// writeGoRepo creates a minimal single-package Go module in a temp dir.
func writeGoRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/smoke\n\ngo 1.25\n")
	write("main.go", "package main\n\nfunc Greet() string { return \"hi\" }\n\nfunc main() { _ = Greet() }\n")
	return dir
}

// TestNewEngine_WiresPlugins asserts that NewEngine registers the full OSS
// explainer set and the renderer, and that the go extractor runs. Explainers
// always run, so their presence in the snapshot meta proves they were wired;
// the extractor list reflects only languages detected in the fixture.
func TestNewEngine_WiresPlugins(t *testing.T) {
	eng := newEngine(t)
	snap, err := eng.GenerateSnapshot(context.Background(), writeGoRepo(t), false)
	if err != nil {
		t.Fatalf("GenerateSnapshot: %v", err)
	}

	wantExplainers := []string{
		"cycles", "layers", "crossrepo", "coverage", "god-class",
		"hotspots", "dependency-depth", "exported-surface", "complexity-outliers",
	}
	for _, name := range wantExplainers {
		if !contains(snap.Meta.Explainers, name) {
			t.Errorf("explainer %q not wired; meta.Explainers = %v", name, snap.Meta.Explainers)
		}
	}

	if !contains(snap.Meta.Extractors, "go") {
		t.Errorf("go extractor did not run; meta.Extractors = %v", snap.Meta.Extractors)
	}
	if !contains(snap.Meta.Renderers, "llm_context") {
		t.Errorf("llm_context renderer not wired; meta.Renderers = %v", snap.Meta.Renderers)
	}
}

func TestNewEngine_SmokeGenerate(t *testing.T) {
	eng := newEngine(t)
	snap, err := eng.GenerateSnapshot(context.Background(), writeGoRepo(t), false)
	if err != nil {
		t.Fatalf("GenerateSnapshot: %v", err)
	}
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	if eng.Store().Count() == 0 {
		t.Error("expected non-empty fact store after generate")
	}
	// The Greet symbol should have been extracted (name format is module-prefixed).
	found := false
	for _, f := range eng.Store().ByKind(facts.KindSymbol) {
		if strings.Contains(f.Name, "Greet") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a symbol fact for Greet")
	}
}

// TestAutoLoadSnapshot verifies that an existing .enola/facts.jsonl is loaded
// into the engine without a generate call. HOME is isolated so no real global
// receipt exists, exercising the single-repo fallback path.
func TestAutoLoadSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	eng, cfg, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: filepath.Join(t.TempDir(), "no-such-config.yaml"),
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	repo := t.TempDir()
	enolaDir := filepath.Join(repo, cfg.Output.Dir)
	if err := os.MkdirAll(enolaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a small facts file using the same serializer production uses.
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "pkg/x", File: "pkg/x", Props: map[string]any{"language": "go"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "pkg/x.Foo", File: "pkg/x/x.go", Line: 1,
			Props: map[string]any{"symbol_kind": "function", "exported": true}},
	)
	if err := store.WriteJSONLFile(filepath.Join(enolaDir, "facts.jsonl")); err != nil {
		t.Fatalf("WriteJSONLFile: %v", err)
	}

	cfg.Repo = repo
	bootstrap.AutoLoadSnapshot(eng, cfg)

	if eng.Store().Count() != 2 {
		t.Errorf("expected 2 facts loaded, got %d", eng.Store().Count())
	}
	if eng.Snapshot() == nil {
		t.Error("expected snapshot to be set after AutoLoadSnapshot")
	}
}

// TestAutoLoadSnapshot_FromGlobalReceipt verifies the multi-repo restore path: a
// prior session's ~/.enola/receipt.json is used to reload the graph on a fresh
// engine, even when cfg.Repo points somewhere with no snapshot. HOME is isolated so
// the receipt written by WriteGlobalReceipt is the only one seen.
func TestAutoLoadSnapshot_FromGlobalReceipt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Session 1: generate a snapshot and record it in the global receipt.
	repo := writeGoRepo(t)
	eng1, cfg, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: filepath.Join(t.TempDir(), "no-such-config.yaml"),
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	snap, err := eng1.GenerateSnapshot(context.Background(), repo, false)
	if err != nil {
		t.Fatalf("GenerateSnapshot: %v", err)
	}
	if err := eng1.WriteArtifacts(repo); err != nil {
		t.Fatalf("WriteArtifacts: %v", err)
	}
	if err := eng1.WriteGlobalReceipt(); err != nil {
		t.Fatalf("WriteGlobalReceipt: %v", err)
	}

	// Session 2 (restart): a brand-new engine whose cfg.Repo has NO snapshot, so a
	// successful restore can only have come from the global receipt.
	eng2, cfg2, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: filepath.Join(t.TempDir(), "no-such-config.yaml"),
	})
	if err != nil {
		t.Fatalf("NewEngine 2: %v", err)
	}
	_ = cfg
	cfg2.Repo = t.TempDir() // empty dir, no .enola
	bootstrap.AutoLoadSnapshot(eng2, cfg2)

	if got := eng2.Store().Count(); got != snap.Meta.FactCount {
		t.Errorf("restored %d facts, want %d", got, snap.Meta.FactCount)
	}
	restored := eng2.Snapshot()
	if restored == nil || restored.Meta.GeneratedAt == "" {
		t.Fatalf("expected a restored snapshot with generated_at, got %+v", restored)
	}
}

// TestNewServer exercises the public server constructor and its accessors,
// which enterprise code relies on to register license-gated tools before Run.
func TestNewServer(t *testing.T) {
	eng, cfg, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: filepath.Join(t.TempDir(), "no-such-config.yaml"),
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	srv, err := bootstrap.NewServer(eng, cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.MCP() == nil {
		t.Error("MCP() returned nil; enterprise code needs it to register extra tools")
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
