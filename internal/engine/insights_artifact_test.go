package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/config"
)

// insightsAfterSnapshot generates a snapshot of repo, writes the artifacts, and
// returns the exact bytes of insights.json plus the hash the receipt recorded for it.
func insightsAfterSnapshot(t *testing.T, e *Engine, repo, outDir string) (string, string) {
	t.Helper()
	if _, err := e.GenerateSnapshot(context.Background(), repo, false); err != nil {
		t.Fatalf("GenerateSnapshot: %v", err)
	}
	if err := e.WriteArtifacts(repo); err != nil {
		t.Fatalf("WriteArtifacts: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(repo, outDir, "insights.json"))
	if err != nil {
		t.Fatalf("reading insights.json: %v", err)
	}

	metaRaw, err := os.ReadFile(filepath.Join(repo, outDir, "snapshot.meta.json"))
	if err != nil {
		t.Fatalf("reading snapshot.meta.json: %v", err)
	}
	var meta struct {
		OutputHashes map[string]string `json:"output_hashes"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatal(err)
	}
	return string(raw), meta.OutputHashes["insights.json"]
}

// The receipt records output_hashes["insights.json"], so the file has to be
// byte-reproducible or comparing two runs — on one machine or across two — fails for
// a reason that has nothing to do with the code being analysed.
//
// The unit-level guarantee lives in explainers/common (MeanStdDev is a function of
// the multiset) and in godclass. This asserts the property a consumer actually sees:
// the same tree, twice, produces the same file.
func TestWriteArtifacts_InsightsAreByteReproducible(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, repo, "go.mod", "module example.com/rep\n\ngo 1.25\n")
	writeRepoFile(t, repo, "core/hub.go", "package core\n\nfunc Hub() string { return \"hub\" }\n")
	for i := range 12 {
		writeRepoFile(t, repo, filepath.Join("callers", "c"+string(rune('a'+i))+".go"),
			"package callers\n\nimport \"example.com/rep/core\"\n\nfunc C"+string(rune('A'+i))+
				"() string { return core.Hub() }\n")
	}

	cfg := config.Default()
	cfg.Repo = repo
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	first, firstHash := insightsAfterSnapshot(t, e, repo, cfg.Output.Dir)
	second, secondHash := insightsAfterSnapshot(t, e, repo, cfg.Output.Dir)

	if first != second {
		t.Errorf("insights.json differs between two runs of an unchanged tree:\n--- first\n%s\n--- second\n%s",
			first, second)
	}
	if firstHash != secondHash {
		t.Errorf("output_hashes[insights.json] = %q then %q — the receipt is not reproducible",
			firstHash, secondHash)
	}
}

// A repository with no findings must still write a JSON array. A nil slice marshals
// to `null`, which breaks any consumer that iterates the parsed value without a nil
// check — on exactly the repositories nobody thinks to test against.
func TestWriteArtifacts_EmptyInsightsAreAnArrayNotNull(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, repo, "go.mod", "module example.com/quiet\n\ngo 1.25\n")
	writeRepoFile(t, repo, "a/a.go", "package a\n\nfunc A() string { return \"a\" }\n")

	cfg := config.Default()
	cfg.Repo = repo
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := insightsAfterSnapshot(t, e, repo, cfg.Output.Dir)
	if raw == "null" {
		t.Fatal("insights.json is `null` for a repository with no findings, not `[]`")
	}
	var parsed []any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("insights.json does not parse as an array: %v\n%s", err, raw)
	}
}
