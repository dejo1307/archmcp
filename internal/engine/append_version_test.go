package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
)

// TestAppend_DiscardsCrossVersionPriorState pins the append-mode version guard:
// a prior state produced by a different extraction behaviour (a different
// cacheVersion, recorded in the snapshot's extractor_version) is discarded
// rather than carried, because the retroactive-tagging migration would
// bulk-claim a stale multi-repo union under one repo's label. The measured
// incident: a v147-pinned union appended to by a v151 build tagged 40,528
// inherited facts — the whole prior union — with the appending repo's label.
func TestAppend_DiscardsCrossVersionPriorState(t *testing.T) {
	repoA := t.TempDir()
	writeFile(t, filepath.Join(repoA, "go.mod"), "module repoa\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repoA, "a.go"), "package repoa\n\nfunc A() {}\n")
	repoB := t.TempDir()
	writeFile(t, filepath.Join(repoB, "go.mod"), "module repob\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repoB, "b.go"), "package repob\n\nfunc B() {}\n")

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	ctx := context.Background()
	if _, err := eng.GenerateSnapshot(ctx, repoA, false); err != nil {
		t.Fatalf("generate repoA: %v", err)
	}
	if err := eng.WriteArtifacts(repoA); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}

	metaPath := filepath.Join(eng.OutputDir(repoA), "snapshot.meta.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	meta["extractor_version"] = "v0-cross-version-test"
	edited, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	labelA := filepath.Base(repoA)
	fresh, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fresh.RegisterExtractor(goextractor.New())
	if err := fresh.RestoreFromDir(eng.OutputDir(repoA), map[string]string{labelA: repoA}, labelA); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if fresh.Store().Count() == 0 {
		t.Fatal("precondition: restored state should hold repoA facts")
	}

	if _, err := fresh.GenerateSnapshot(ctx, repoB, true); err != nil {
		t.Fatalf("append repoB: %v", err)
	}
	labelB := filepath.Base(repoB)
	for _, f := range fresh.Store().All() {
		if f.Repo == labelA || strings.HasPrefix(f.File, labelA+"/") {
			t.Fatalf("cross-version prior fact carried into append: repo=%q file=%q", f.Repo, f.File)
		}
		if f.Repo != "" && f.Repo != labelB {
			t.Fatalf("unexpected repo label %q on fact %q", f.Repo, f.Name)
		}
	}
}

func TestWalkRepo_SymlinkedRootExtracts(t *testing.T) {
	real := t.TempDir()
	writeFile(t, filepath.Join(real, "go.mod"), "module real\n\ngo 1.21\n")
	writeFile(t, filepath.Join(real, "r.go"), "package real\n\nfunc R() {}\n")
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "aliased-label")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	snap, err := eng.GenerateSnapshot(context.Background(), link, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.FactCount == 0 {
		t.Fatal("a symlinked repo root must extract — WalkDir Lstats the root and walks nothing without resolution")
	}
}
