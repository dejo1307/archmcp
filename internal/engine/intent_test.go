package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
	"github.com/enola-labs/enola/internal/extractors/mdintent"
	"github.com/enola-labs/enola/internal/intent"
)

func writeGoRepo(t *testing.T) string {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module m\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repo, "m.go"), "package m\n\nfunc M() {}\n")
	return repo
}

func TestIntent_RepoFileDiscoveredAndCarried(t *testing.T) {
	repo := writeGoRepo(t)
	writeFile(t, filepath.Join(repo, intent.RepoFileName),
		"service:\n  name: m\nconsumes:\n  - repo: other\n    via: http-client\n")
	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	if _, err := eng.GenerateSnapshot(context.Background(), repo, false); err != nil {
		t.Fatal(err)
	}
	d := eng.Intent(filepath.Base(repo))
	if d == nil || d.Service.Name != "m" || d.Overridden {
		t.Fatalf("repo-file intent not carried: %+v", d)
	}
	if d.Source != intent.RepoFileName {
		t.Fatalf("source = %q, want the repo-relative file name", d.Source)
	}
}

func TestIntent_ClusterEntryOverridesWholesale(t *testing.T) {
	repo := writeGoRepo(t)
	writeFile(t, filepath.Join(repo, intent.RepoFileName),
		"consumes:\n  - repo: from-file\n    via: http\n")
	cfg := config.Default()
	cfg.Intent = map[string]*intent.Declaration{
		filepath.Base(repo): {Consumes: []intent.Seam{{Repo: "from-cluster", Via: "graphql"}}},
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	if _, err := eng.GenerateSnapshot(context.Background(), repo, false); err != nil {
		t.Fatal(err)
	}
	d := eng.Intent(filepath.Base(repo))
	if d == nil || !d.Overridden || d.Source != intent.ClusterSource {
		t.Fatalf("override not recorded: %+v", d)
	}
	if len(d.Consumes) != 1 || d.Consumes[0].Repo != "from-cluster" {
		t.Fatalf("override must be wholesale: %+v", d.Consumes)
	}
}

func TestIntent_InvalidRepoFileFailsTheSnapshot(t *testing.T) {
	repo := writeGoRepo(t)
	writeFile(t, filepath.Join(repo, intent.RepoFileName),
		"consumes:\n  - repo: x\n    via: rest\n")
	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	if _, err := eng.GenerateSnapshot(context.Background(), repo, false); err == nil {
		t.Fatal("an invalid declaration must fail the snapshot, never silently skip")
	}
}

func TestIntent_ConfigLoadValidatesEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(path, []byte("repo: .\nintent:\n  m:\n    consumes:\n      - repo: x\n        via: bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("an invalid cluster intent entry must fail config load")
	}
}

// Both declaration carriers fail the snapshot alike. The repo file already did;
// the markdown carrier reached the engine through the extractor path, whose
// errors are recorded and stepped over — so an invalid block published a
// successful run that had quietly lost every verdict the wiki declared, and a
// gate reading those verdicts passed for want of anything left to fail on.
func TestIntent_InvalidPageFailsTheSnapshot(t *testing.T) {
	repo := writeGoRepo(t)
	writeFile(t, filepath.Join(repo, "decision.md"),
		"---\nenola_intent:\n  consumes:\n    - {repo: a, target: b, via: rest}\n---\nbody\n")
	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExtractor(mdintent.New())
	_, err = eng.GenerateSnapshot(context.Background(), repo, false)
	if err == nil {
		t.Fatal("an invalid enola_intent block must fail the snapshot, not degrade it")
	}
	if !strings.Contains(err.Error(), "decision.md") || !strings.Contains(err.Error(), "graphql") {
		t.Fatalf("the error must name the page and the allowed set, got: %v", err)
	}
}

// Declarations ride the published bundle, so append mode has to carry them the
// way it carries repoPaths — and a repo that stops declaring has to stop being
// declared, including when its entry came from the previous bundle.
func TestIntent_AppendCarriesDeclarationsAndDropsWithdrawn(t *testing.T) {
	a := writeGoRepo(t)
	b := writeGoRepo(t)
	writeFile(t, filepath.Join(a, intent.RepoFileName), "service:\n  name: a\n")
	writeFile(t, filepath.Join(b, intent.RepoFileName), "service:\n  name: b\n")
	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	if _, err := eng.GenerateSnapshot(context.Background(), a, false); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.GenerateSnapshot(context.Background(), b, true); err != nil {
		t.Fatal(err)
	}
	if d := eng.Intent(filepath.Base(a)); d == nil || d.Service.Name != "a" {
		t.Fatalf("append must carry the earlier repo's declaration, got %+v", d)
	}
	if d := eng.Intent(filepath.Base(b)); d == nil || d.Service.Name != "b" {
		t.Fatalf("append must record the appended repo's declaration, got %+v", d)
	}

	// a withdraws its declaration and is re-snapshotted into the same graph.
	if err := os.Remove(filepath.Join(a, intent.RepoFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.GenerateSnapshot(context.Background(), a, true); err != nil {
		t.Fatal(err)
	}
	if d := eng.Intent(filepath.Base(a)); d != nil {
		t.Fatalf("a withdrawn declaration must not survive in the bundle, got %+v", d)
	}
	if d := eng.Intent(filepath.Base(b)); d == nil || d.Service.Name != "b" {
		t.Fatalf("withdrawing one repo's declaration must not disturb another's, got %+v", d)
	}
}
