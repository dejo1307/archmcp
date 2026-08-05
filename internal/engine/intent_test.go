package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
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
