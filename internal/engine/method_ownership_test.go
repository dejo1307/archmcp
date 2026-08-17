package engine_test

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/extractors"
	"github.com/enola-labs/enola/internal/extractors/rustextractor"
	"github.com/enola-labs/enola/internal/extractors/tsextractor"
	"github.com/enola-labs/enola/internal/facts"
)

// The two cases here are driven through the real extractors rather than written as
// fact literals, because what they are about is the SHAPE of the names extractors
// produce. A hand-built store would encode the author's belief about that shape,
// which is the belief under test: owner resolution reads "#" as the Ruby method
// separator, and both of these names carry a "#" that separates nothing.

// hasMethodEdges returns every synthesized has_method edge in the snapshot's graph
// as "owner -> method", sorted.
func hasMethodEdges(t *testing.T, eng *engine.Engine) []string {
	t.Helper()
	store := eng.Store()
	graph := store.Graph()
	if graph == nil {
		t.Fatal("snapshot produced no graph")
	}
	seen := map[string]bool{}
	var edges []string
	for _, symbol := range store.ByKind(facts.KindSymbol) {
		if seen[symbol.Name] {
			continue
		}
		seen[symbol.Name] = true
		for _, edge := range graph.ForwardEdges(symbol.Name) {
			if edge.RelKind == facts.RelHasMethod {
				edges = append(edges, symbol.Name+" -> "+edge.Target)
			}
		}
	}
	sort.Strings(edges)
	return edges
}

func snapshotWith(t *testing.T, repo string, extractor extractors.Extractor) *engine.Engine {
	t.Helper()
	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(extractor)
	if _, err := eng.GenerateSnapshot(context.Background(), repo, false); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return eng
}

func requireEdges(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("has_method edges = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("has_method edges = %v, want %v", got, want)
		}
	}
}

// TestHasMethod_RustRawIdentifierKeepsItsOwner — Rust escapes a keyword used as an
// identifier with an "r#" prefix, and `fn r#async` is ordinary Rust (r#type, r#match
// and r#move likewise; ripgrep's own crates/cli/src/process.rs has one). The method
// fact is named "src.Reader.r#async", so reading "#" as the owner separator asks for
// a type called "src.Reader.r" that no fact declares, and the method loses its class.
func TestHasMethod_RustRawIdentifierKeepsItsOwner(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Cargo.toml"), "[package]\nname = \"fx\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFile(t, filepath.Join(repo, "src", "lib.rs"), `pub struct Reader {
    pub inner: u32,
}

impl Reader {
    pub fn r#async(&self) -> u32 {
        self.inner
    }

    pub fn sync(&self) -> u32 {
        self.inner
    }
}
`)

	eng := snapshotWith(t, repo, rustextractor.New())

	requireEdges(t, hasMethodEdges(t, eng), []string{
		"src.Reader -> src.Reader.r#async",
		"src.Reader -> src.Reader.sync",
	})
}

// TestHasMethod_HashInADirectoryNameKeepsTheSubtree — TypeScript, Python, Kotlin,
// Swift and Java all prefix a symbol name with its directory, so a single "#" in a
// directory name puts one into every symbol beneath it. Reading that "#" as the owner
// separator does not misattribute the subtree, it silently deletes it: every class
// under the directory loses every method, with no error and nothing in the receipt.
func TestHasMethod_HashInADirectoryNameKeepsTheSubtree(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "tsconfig.json"), "{\n  \"compilerOptions\": { \"target\": \"es2020\" }\n}\n")
	writeFile(t, filepath.Join(repo, "src", "v1#legacy", "vault.ts"), `export class Vault {
  open(): number {
    return 1;
  }

  close(): number {
    return 2;
  }

  seal(): number {
    return 3;
  }
}
`)
	writeFile(t, filepath.Join(repo, "src", "plain", "ok.ts"), `export class Ok {
  run(): number {
    return 4;
  }
}
`)

	eng := snapshotWith(t, repo, tsextractor.New())

	requireEdges(t, hasMethodEdges(t, eng), []string{
		"src/plain.Ok -> src/plain.Ok.run",
		"src/v1#legacy.Vault -> src/v1#legacy.Vault.close",
		"src/v1#legacy.Vault -> src/v1#legacy.Vault.open",
		"src/v1#legacy.Vault -> src/v1#legacy.Vault.seal",
	})
}
