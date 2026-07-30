package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
	"github.com/enola-labs/enola/internal/facts"
)

// driftEngine snapshots a two-package Go repo and returns the engine holding it.
func driftEngine(t *testing.T, repo string) *engine.Engine {
	t.Helper()
	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	if _, err := eng.GenerateSnapshot(context.Background(), repo, false); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return eng
}

func driftRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module driftmod\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repo, "pkg", "a", "a.go"), "package a\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(repo, "pkg", "b", "b.go"), "package b\n\nfunc B() {}\n")
	return repo
}

// TestDrift_UnchangedTreeHasNoDrift is the false-positive guard: enola writes its own
// output dir during the snapshot, so a naive comparison would report that as drift on
// every call. The walker's ignore globs must keep it out.
func TestDrift_UnchangedTreeHasNoDrift(t *testing.T) {
	repo := driftRepo(t)
	eng := driftEngine(t, repo)
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}

	d, err := eng.Drift(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d.Any() {
		t.Errorf("untouched tree reported drift: added=%v removed=%v modified=%v", d.Added, d.Removed, d.Modified)
	}
}

// TestDrift_DetectsModifiedFile is the base case: content changed, nothing else.
func TestDrift_DetectsModifiedFile(t *testing.T) {
	repo := driftRepo(t)
	eng := driftEngine(t, repo)

	writeFile(t, filepath.Join(repo, "pkg", "a", "a.go"), "package a\n\nfunc A() {}\nfunc Added() {}\n")

	d, err := eng.Drift(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Any() {
		t.Fatal("edited file produced no drift")
	}
	if len(d.Modified) != 1 || d.Modified[0] != filepath.Join("pkg", "a", "a.go") {
		t.Errorf("Modified=%v, want exactly [pkg/a/a.go]", d.Modified)
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Errorf("unexpected added=%v removed=%v", d.Added, d.Removed)
	}
}

// TestDrift_DetectsAddedAndRemoved covers the set difference, not just content.
func TestDrift_DetectsAddedAndRemoved(t *testing.T) {
	repo := driftRepo(t)
	eng := driftEngine(t, repo)

	writeFile(t, filepath.Join(repo, "pkg", "c", "c.go"), "package c\n\nfunc C() {}\n")
	if err := os.Remove(filepath.Join(repo, "pkg", "b", "b.go")); err != nil {
		t.Fatal(err)
	}

	d, err := eng.Drift(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Added) != 1 || d.Added[0] != filepath.Join("pkg", "c", "c.go") {
		t.Errorf("Added=%v, want [pkg/c/c.go]", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0] != filepath.Join("pkg", "b", "b.go") {
		t.Errorf("Removed=%v, want [pkg/b/b.go]", d.Removed)
	}
}

// TestDrift_DirtyAtSnapshotStillDetectsFurtherEdits is the whole point of this change,
// and the case the git-boolean signal cannot express (new/74). The snapshot is taken
// over a tree that is ALREADY modified; a later edit to a DIFFERENT file must still be
// detected. No git involved — drift is a filesystem question, and this repo is not even
// a git repo.
func TestDrift_DirtyAtSnapshotStillDetectsFurtherEdits(t *testing.T) {
	repo := driftRepo(t)

	// Pre-existing "uncommitted work" at snapshot time.
	writeFile(t, filepath.Join(repo, "pkg", "a", "a.go"), "package a\n\nfunc A() {}\nfunc WorkInProgress() {}\n")
	eng := driftEngine(t, repo)

	if d, err := eng.Drift(repo); err != nil {
		t.Fatal(err)
	} else if d.Any() {
		t.Fatalf("precondition: snapshot should match the tree it was taken over, got %+v", d)
	}

	// Now drift a different file.
	writeFile(t, filepath.Join(repo, "pkg", "b", "b.go"), "package b\n\nfunc B() {}\nfunc Later() {}\n")

	d, err := eng.Drift(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Modified) != 1 || d.Modified[0] != filepath.Join("pkg", "b", "b.go") {
		t.Errorf("Modified=%v, want [pkg/b/b.go] — a snapshot taken over an already-modified tree must still detect later edits", d.Modified)
	}
}

// TestDrift_NoRecordedHashesIsUnknown guards the degradation path: a snapshot with no
// FileHashes (pre-receipt, or a restore whose meta failed to load) must report "cannot
// verify" rather than a false all-clear.
func TestDrift_NoRecordedHashesIsUnknown(t *testing.T) {
	repo := driftRepo(t)
	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// A snapshot with RepoPath but no FileHashes, as AutoLoadSnapshot used to produce.
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{RepoPath: repo}})

	d, err := eng.Drift(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Unknown {
		t.Error("a snapshot with no recorded file hashes must report Unknown, not a clean tree")
	}
	if d.Any() {
		t.Errorf("Unknown drift must not claim specific changes: %+v", d)
	}
}
