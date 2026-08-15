package check_test

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
	"github.com/enola-labs/enola/pkg/check"
)

// checkPolicyCycles names the cycles explainer explicitly. These end-to-end tests are
// about the gate mechanics — a new cycle fails, a pre-existing one does not — and an
// empty Policy now enforces nothing, so the policy has to be stated for them to mean
// anything. See check.DefaultFailExplainers.
func checkPolicyCycles() check.Policy {
	return check.Policy{FailExplainers: []string{"cycles"}}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo builds a two-package Go module with a single a→b import and returns an engine
// primed with the Go extractor and the cycles explainer.
func newRepo(t *testing.T) (string, *engine.Engine) {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module testmod\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repo, "pkg", "a", "a.go"), "package a\n\nimport \"testmod/pkg/b\"\n\nfunc A() { b.B() }\n")
	writeFile(t, filepath.Join(repo, "pkg", "b", "b.go"), "package b\n\nfunc B() {}\n")

	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExplainer(cycles.New())
	return repo, eng
}

// currentOf builds the "current" snapshot the way the CLI does — from the live store,
// so it reflects the whole graph rather than only the last repo indexed.
func currentOf(eng *engine.Engine) *facts.Snapshot {
	snap := eng.Snapshot()
	return &facts.Snapshot{Meta: snap.Meta, Facts: eng.Store().All(), Insights: snap.Insights}
}

// TestGate_NewCycleFailsTheBuild is the whole loop end to end on a real repository:
// snapshot → pin baseline → introduce an import cycle → re-snapshot → grade. The gate
// must exit 1, and it must name the cycle.
func TestGate_NewCycleFailsTheBuild(t *testing.T) {
	repo, eng := newRepo(t)
	ctx := context.Background()

	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	if err := eng.SetBaseline(repo); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	// b now imports a, closing the loop a→b→a.
	writeFile(t, filepath.Join(repo, "pkg", "b", "b.go"),
		"package b\n\nimport \"testmod/pkg/a\"\n\nfunc B() {}\n\nfunc B2() { a.A() }\n")

	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	base, err := engine.LoadSnapshotDir(engine.ResolveBaselineDir(eng.OutputDir(repo), "pinned"))
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}

	v := check.Evaluate(diff.Compute(base, currentOf(eng)), checkPolicyCycles())

	if v.Status != check.StatusRegression {
		t.Errorf("status = %q, want %q\n%s", v.Status, check.StatusRegression, v.Render())
	}
	if v.ExitCode() != 1 {
		t.Errorf("exit = %d, want 1", v.ExitCode())
	}
	if len(v.Failures) == 0 {
		t.Fatalf("no failures recorded\n%s", v.Render())
	}
	if src := v.Failures[0].Source; src != "cycles" {
		t.Errorf("failure source = %q, want %q", src, "cycles")
	}
	if out := v.Render(); !strings.Contains(strings.ToLower(out), "cyclic") {
		t.Errorf("render did not name the cycle:\n%s", out)
	}
}

// TestGate_NoChangeIsClean covers the case a gate is asked to answer most often, and
// the one where a false positive is most damaging: nothing changed.
//
// It also pins the same-second regression. GeneratedAt has second resolution, so a
// baseline pinned and re-diffed inside one second yields a zero gap; while that counted
// as "the baseline is newer than the current snapshot", this exited non-zero on an
// untouched repository.
func TestGate_NoChangeIsClean(t *testing.T) {
	repo, eng := newRepo(t)
	ctx := context.Background()

	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	if err := eng.SetBaseline(repo); err != nil {
		t.Fatalf("set baseline: %v", err)
	}
	// Re-snapshot with the source untouched.
	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	base, err := engine.LoadSnapshotDir(engine.ResolveBaselineDir(eng.OutputDir(repo), "pinned"))
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}

	v := check.Evaluate(diff.Compute(base, currentOf(eng)), checkPolicyCycles())

	if v.Status != check.StatusClean {
		t.Errorf("status = %q, want %q — an unchanged repo must pass\n%s", v.Status, check.StatusClean, v.Render())
	}
	if v.ExitCode() != 0 {
		t.Errorf("exit = %d, want 0\n%s", v.ExitCode(), v.Render())
	}
	if len(v.BlockingKinds) > 0 {
		t.Errorf("unchanged repo reported blocking comparability: %v\n%s", v.BlockingKinds, v.Render())
	}
}

// TestGate_PreExistingCycleIsNotARegression is the ratchet property the gate inherits
// from the diff: a cycle that was already there before the change must not fail the
// build, or the gate would be unusable on any repository that is not already perfect.
func TestGate_PreExistingCycleIsNotARegression(t *testing.T) {
	repo, eng := newRepo(t)
	ctx := context.Background()

	// Start WITH the cycle already in place.
	writeFile(t, filepath.Join(repo, "pkg", "b", "b.go"),
		"package b\n\nimport \"testmod/pkg/a\"\n\nfunc B() {}\n\nfunc B2() { a.A() }\n")

	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	if err := eng.SetBaseline(repo); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	// An unrelated edit that leaves the cycle exactly as it was.
	writeFile(t, filepath.Join(repo, "pkg", "c", "c.go"), "package c\n\nfunc C() {}\n")

	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	base, err := engine.LoadSnapshotDir(engine.ResolveBaselineDir(eng.OutputDir(repo), "pinned"))
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}

	v := check.Evaluate(diff.Compute(base, currentOf(eng)), checkPolicyCycles())

	if v.Status != check.StatusClean {
		t.Errorf("status = %q, want %q — a pre-existing cycle is not a regression\n%s",
			v.Status, check.StatusClean, v.Render())
	}
}
