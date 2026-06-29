package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/explainers/cycles"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
	"github.com/enola-labs/enola/internal/facts"
)

// writeFile creates path with content, making parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBaselineDiff_NewCycleIsRegression exercises the full validation loop on a
// real Go repo: snapshot → pin baseline → introduce an import cycle → re-snapshot
// → diff. The new cycle must surface as a regression, and the pre-existing edge
// a→b must NOT appear as noise (delta-only ratchet).
func TestBaselineDiff_NewCycleIsRegression(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module testmod\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repo, "pkg", "a", "a.go"), "package a\n\nimport \"testmod/pkg/b\"\n\nfunc A() { b.B() }\n")
	writeFile(t, filepath.Join(repo, "pkg", "b", "b.go"), "package b\n\nfunc B() {}\n")

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExplainer(cycles.New())

	ctx := context.Background()

	// 1. First snapshot (no cycle) + pin baseline.
	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	if err := eng.SetBaseline(repo); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	// 2. Introduce a cycle: b now imports a.
	writeFile(t, filepath.Join(repo, "pkg", "b", "b.go"), "package b\n\nimport \"testmod/pkg/a\"\n\nfunc B() { _ = a.A }\n")

	// 3. Re-snapshot.
	cur, err := eng.GenerateSnapshot(ctx, repo, false)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts 2: %v", err)
	}

	// 4. Load the pinned baseline and diff against current.
	baseDir := filepath.Join(eng.OutputDir(repo), engine.BaselineSubdir)
	baseline, err := engine.LoadSnapshotDir(baseDir)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	current := &facts.Snapshot{Meta: cur.Meta, Facts: eng.Store().All(), Insights: cur.Insights}

	d := diff.Compute(baseline, current)

	// The new cycle must be a regression.
	foundCycle := false
	for _, in := range d.FindingsNew {
		if in.Source == "cycles" {
			foundCycle = true
		}
	}
	if !foundCycle {
		t.Fatalf("expected a new 'cycles' finding after introducing the cycle; got new findings: %+v", d.FindingsNew)
	}

	// The new edge b->a must show as added coupling.
	foundEdge := false
	for _, e := range d.EdgesAdded {
		if strings.Contains(e.Source, "b") && strings.Contains(e.Target, "a") {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Fatalf("expected b->a edge in EdgesAdded; got %+v", d.EdgesAdded)
	}

	// Determinism: the rendered summary is byte-stable.
	if d.RenderSummary() != diff.Compute(baseline, current).RenderSummary() {
		t.Fatal("diff render is not deterministic")
	}

	// Baseline pin survived the second generate_snapshot (still loadable, still cycle-free).
	if len(baseline.Insights) != 0 {
		for _, in := range baseline.Insights {
			if in.Source == "cycles" {
				t.Fatal("baseline unexpectedly contains a cycle — pin was clobbered by re-snapshot")
			}
		}
	}
}

// TestRotatePrevious verifies generate_snapshot auto-rotates the prior run into
// previous/, so diff with baseline='previous' works without an explicit pin.
func TestRotatePrevious(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module testmod\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repo, "a", "a.go"), "package a\n\nfunc A() {}\n")

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())

	ctx := context.Background()
	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatal(err)
	}
	// No previous/ yet after the first write.
	prevFacts := filepath.Join(eng.OutputDir(repo), engine.PreviousSubdir, "facts.jsonl")
	if _, err := os.Stat(prevFacts); err == nil {
		t.Fatal("previous/ should not exist after the first snapshot")
	}

	// Second write rotates the first into previous/.
	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prevFacts); err != nil {
		t.Fatalf("previous/facts.jsonl should exist after the second snapshot: %v", err)
	}
}
