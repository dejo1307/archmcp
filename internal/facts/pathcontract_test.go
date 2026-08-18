package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The enforcement behind the fact-path invariant: a repo-relative path in a fact is
// forward-slash on every host.
//
// The invariant needs enforcement rather than documentation because breaking it is
// SILENT. A backslash path errors nowhere — it parses, it stores, it renders. It
// simply stops being equal to the path everything else derived, so module resolution
// misses, layer classification finds nothing, and a declared layer order reports
// itself in force while governing zero modules. That is issue #242, and it reached a
// release because nothing in the tree could tell the two dialects apart.
//
// Three checks, each catching the failure at a different distance:
//
//   - TestPathContract_NoHostPathBuildersOnFactPaths fails the moment the code is
//     written, on any host. It is the one that matters: the other two can only see a
//     regression that a Windows run actually produced.
//   - TestPathContract_HostRelativePathsAreNormalisedAtSource fails when an extractor
//     walks the filesystem itself and forgets to convert what filepath.Rel returns.
//   - TestPathContract_GoldenFactPathsAreSlashed fails when a golden captures one.
//
// All three are pure file scans with no engine or tree-sitter dependency, so they run
// in milliseconds — cheap enough for a pre-push hook, like internal/cachecov.

// factPathTrees are the packages that BUILD fact paths. Everything here works in
// repo-relative space and must use internal/factpath; `path/filepath` is the host
// filesystem's dialect and belongs to code that opens files.
var factPathTrees = []string{
	filepath.Join("internal", "extractors"),
	filepath.Join("internal", "linkers"),
	filepath.Join("internal", "explainers"),
}

// hostPathBuilders are the filepath operations whose OUTPUT carries the host
// separator, so a fact built from one is backslash-flavoured on Windows.
//
// filepath.Base and filepath.Ext are deliberately absent: their output is a single
// segment with no separator in it, and their input handling accepts "/" and "\" alike
// on Windows, so both are already correct on either dialect. Forbidding them would be
// noise, and a check that flags correct code stops being read.
//
// filepath.Join is absent for a different reason: joining a repo path to a relative
// one is how a file gets OPENED, which is exactly what filepath is for. It is caught
// indirectly — a Join whose result becomes a fact has to pass through Dir or Clean
// first in every shape this codebase uses.
var hostPathBuilders = regexp.MustCompile(`\bfilepath\.(Dir|Clean|Split|Match)\(`)

// hostMarker exempts a single line: the operation genuinely works on a host path.
// Written as a comment on the line itself so the exemption is read next to what it
// exempts, and so adding one is a visible act in review rather than an edit to a list
// somewhere else.
const hostMarker = "//factpath:host"

func TestPathContract_NoHostPathBuildersOnFactPaths(t *testing.T) {
	root := repoRoot(t)
	for _, tree := range factPathTrees {
		scanGoFiles(t, filepath.Join(root, tree), func(rel string, num int, line string) {
			if !hostPathBuilders.MatchString(line) || strings.Contains(line, hostMarker) {
				return
			}
			t.Errorf("%s:%d: builds a path with path/filepath, whose output carries the host separator.\n"+
				"\tUse internal/factpath (factpath.Dir/Clean/Split/Match) for a repo-relative fact path.\n"+
				"\tIf this really is a host path — an absolute one, or the repo root — end the line with %s.\n"+
				"\t%s", rel, num, hostMarker, strings.TrimSpace(line))
		})
	}
}

// relAssign matches the two-value assignment from filepath.Rel, capturing the variable
// the relative path lands in.
var relAssign = regexp.MustCompile(`(\w+),\s*\w+\s*:?=\s*filepath\.Rel\(`)

// TestPathContract_HostRelativePathsAreNormalisedAtSource asserts that every extractor
// that walks the filesystem itself converts what filepath.Rel hands back.
//
// filepath.Rel is the OTHER door the host dialect comes through, and it is the one the
// engine's own walker was letting it through. Extractors that walk independently (to
// read a pubspec, a schema, a config off-glob) each have their own copy of that door.
// The rule is that the conversion happens on the assignment or within a couple of
// lines of it — close enough that an error check may sit between — before the variable
// can be handed anywhere. A normalisation further down is not wrong, but it is one edit
// away from being missed, and this check exists because that class of miss is invisible
// in review.
func TestPathContract_HostRelativePathsAreNormalisedAtSource(t *testing.T) {
	root := repoRoot(t)
	for _, tree := range factPathTrees {
		dir := filepath.Join(root, tree)
		forEachGoFile(t, dir, func(rel string, lines []string) {
			for i, line := range lines {
				m := relAssign.FindStringSubmatch(line)
				if m == nil || strings.Contains(line, hostMarker) {
					continue
				}
				variable := m[1]
				normalised := regexp.MustCompile(
					`(factpath\.Slash|filepath\.ToSlash)\(\s*` + regexp.QuoteMeta(variable) + `\s*\)`)
				window := lines[i:min(i+6, len(lines))]
				if normalised.MatchString(strings.Join(window, "\n")) {
					continue
				}
				t.Errorf("%s:%d: %q comes back from filepath.Rel in the host's dialect and is not converted.\n"+
					"\tWrap it in factpath.Slash on the assignment, so no later line has to remember.\n"+
					"\tIf the value is only ever compared against another host path, end the line with %s.\n"+
					"\t%s", rel, i+1, variable, hostMarker, strings.TrimSpace(line))
			}
		})
	}
}

// TestPathContract_GoldenFactPathsAreSlashed asserts no committed golden carries a
// backslash where a path belongs.
//
// On Linux and macOS this passes trivially, which is the point: run the same goldens
// on Windows (CI does) and it is the check that fails, naming the fact. It is the
// backstop for a leak that gets past both source scans above.
//
// PHP is the reason this cannot simply forbid backslashes everywhere. PHP namespaces
// are backslash-separated, so `App\Http\Controllers\UserController` is a correct
// symbol name and `Illuminate\Support\Facades\Route` a correct dependency target. The
// fields checked here are exactly those that can only ever hold a path.
func TestPathContract_GoldenFactPathsAreSlashed(t *testing.T) {
	goldenDir := filepath.Join(repoRoot(t), "internal", "engine", "testdata", "golden")
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatalf("reading golden dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(goldenDir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var f Fact
			if err := json.Unmarshal([]byte(line), &f); err != nil {
				t.Fatalf("%s:%d: not a fact: %v", entry.Name(), i+1, err)
			}
			if strings.Contains(f.File, `\`) {
				t.Errorf("%s:%d: fact file %q carries a host separator; fact paths are forward-slash on every host",
					entry.Name(), i+1, f.File)
			}
			if pathShapedName[f.Kind] && strings.Contains(f.Name, `\`) {
				t.Errorf("%s:%d: %s fact is named %q, and a %s name is a path — forward slashes on every host",
					entry.Name(), i+1, f.Kind, f.Name, f.Kind)
			}
		}
	}
}

// scanGoFiles calls visit for every non-test line of every Go file under dir, skipping
// comments: prose explains the distinction this file enforces and must stay readable.
func scanGoFiles(t *testing.T, dir string, visit func(rel string, num int, line string)) {
	t.Helper()
	forEachGoFile(t, dir, func(rel string, lines []string) {
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			visit(rel, i+1, line)
		}
	})
}

func forEachGoFile(t *testing.T, dir string, visit func(rel string, lines []string)) {
	t.Helper()
	root := repoRoot(t)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		visit(filepath.ToSlash(rel), strings.Split(string(src), "\n"))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
}
