package history

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// T-AUTH-1 (see _BUILDING_HISTORY.md §2): nothing that judges the PRESENT may read the
// history.
//
// docs/SNAPSHOTS.md rests on a claim about authority rather than persistence — every file
// enola writes is derivable from the tree, and none of them accumulates a history the
// source has forgotten. The history breaks the second half of that on purpose, which is
// exactly why the first half has to be defended by something other than good intentions:
// the moment a verdict about the current tree consults an accumulated log, "a clean diff
// means something" stops being true, and it stops being true silently.
//
// DIRECT imports, deliberately, and not the transitive closure. pkg/check reaches
// internal/engine, which records history at the end of WriteArtifacts, so a transitive
// rule could never hold and would have to be either abandoned or exempted into
// meaninglessness. What is assertable — and what actually prevents the failure — is that
// the code computing a verdict does not NAME the history. The behavioural half of the
// invariant, that deleting the history changes no answer, is
// TestHistory_DeletingItChangesNothingAboutThePresent in internal/engine.
func TestVerdictPathsDoNotReadTheHistory(t *testing.T) {
	root := repoRoot(t)

	// Everything here answers a question about the tree as it is now.
	targets := []string{
		"pkg/check",                    // the gate: is this change a regression?
		"internal/diff",                // the comparator every verdict is computed from
		"pkg/coverage",                 // what did the linker resolve, now?
		"internal/engine/freshness.go", // is the snapshot still current?
		"internal/engine/drift.go",     // has the tree moved since the snapshot?
	}

	for _, target := range targets {
		for _, file := range goFiles(t, filepath.Join(root, target)) {
			for _, imp := range importsOf(t, file) {
				if strings.HasSuffix(imp, "/history") || strings.Contains(imp, "/history/") {
					rel, _ := filepath.Rel(root, file)
					t.Errorf("%s imports %s — a verdict about the present must not read the history", rel, imp)
				}
			}
		}
	}
}

// repoRoot walks up from this package to the module root, so the test does not depend on
// where `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the module root")
	return ""
}

// goFiles returns the non-test Go files at path, which may name a file or a directory.
// Test files are excluded: a test may legitimately read a history to assert something
// about it, and only production code can carry the defect this guards against.
func goFiles(t *testing.T, path string) []string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("target %s is missing — this test names paths, so a rename must fail it loudly: %v", path, err)
	}
	if !info.IsDir() {
		return []string{path}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(path, name))
	}
	return files
}

func importsOf(t *testing.T, file string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		out = append(out, path)
	}
	return out
}
