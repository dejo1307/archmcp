package estateref

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// treeRoot is the folder this package sits two levels inside. The whole folder
// is what a contribution is split from, so the whole folder is what is checked.
const treeRoot = "../.."

// termsPath is the denylist, which sits outside the tree deliberately: written
// inside it, it would travel with it. Nothing else in this package needs it —
// every rule below is exercised against names invented for the test — so a
// copy of this tree standing on its own loses the sweep and keeps the rest.
const termsPath = treeRoot + "/../harness/estate-terms.txt"

// invented are names that resemble the real ones in shape and name nothing.
// The rules are what these tests are about, and pinning them against invented
// names is what lets this file travel with the tree it guards.
var invented = []Term{
	{Text: "acmecorp"},
	{Text: "acme-tools"},
	{Text: "anchor", Word: true},
}

// The gate. It fails on the file, the line and the term, because a refresh that
// reintroduces a name reintroduces several at once and a count alone would send
// the reader hunting.
func TestTreeNamesNoPrivateRepository(t *testing.T) {
	terms := listedTerms(t)
	hits, err := Scan(treeRoot, terms)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, hit := range hits {
		t.Errorf("%s", hit)
	}
	if len(hits) > 0 {
		t.Fatalf("%d identifying reference(s): neutralise the identity and keep the measurement verbatim, "+
			"or extend the denylist if a name has become one this tree may carry", len(hits))
	}
}

// Every name on the list is live. A denylist nobody exercises is a list of good
// intentions: this plants each entry in turn, shouted, and requires the scan to
// report it at the file and line it was planted on.
func TestEveryListedNameIsLive(t *testing.T) {
	for _, term := range listedTerms(t) {
		t.Run(term.Text, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"internal/explainers/x.go": "package explainers\n\n// nothing on this line\n" +
					"// measured over " + strings.ToUpper(term.Text) + ": 3,392 of 3,732 unmatched\n" +
					"const n = 200\n",
			})

			hits := scan(t, root, []Term{term})
			if len(hits) != 1 {
				t.Fatalf("hits = %+v, want exactly the planted one", hits)
			}
			got := hits[0]
			if got.File != "internal/explainers/x.go" || got.Line != 4 || got.Term != term.Text {
				t.Errorf("hit = %+v, want internal/explainers/x.go:4 %q", got, term.Text)
			}
			// Case-insensitively: a name is the same name shouted.
			if !strings.Contains(got.Text, strings.ToUpper(term.Text)) {
				t.Errorf("hit text = %q, want the line it matched on", got.Text)
			}
		})
	}
}

// A name inherited verbatim from pristine upstream is exempt where it is
// inherited and nowhere else. The exemption is a path prefix, so the same name
// one directory over is still reported — the alternative, leaving the name off
// the list, would leave the whole tree unguarded against it and record no
// reason why.
func TestListedExemptionsAreScopedToTheirPath(t *testing.T) {
	for _, term := range listedTerms(t) {
		for _, prefix := range term.Except {
			t.Run(term.Text, func(t *testing.T) {
				body := "package p\n\n// " + term.Text + "\n"
				root := writeTree(t, map[string]string{
					prefix + "p.go":                    body,
					filepath.Dir(prefix) + "-two/p.go": body,
				})

				hits := scan(t, root, []Term{term})
				if len(hits) != 1 {
					t.Fatalf("hits = %+v, want only the one outside %s", hits, prefix)
				}
				if strings.HasPrefix(hits[0].File, prefix) {
					t.Errorf("hit = %+v, want the inherited copy left alone", hits[0])
				}
			})
		}
	}
}

// A coined name is the same name however it is spelled. The trailing word
// boundary this check first shipped with passed every one of these, and a test
// asserting that it did read as though the hole were the design.
func TestScanCatchesNamesInsideIdentifiers(t *testing.T) {
	spellings := []string{
		"AcmecorpClient",
		"acmecorp_models",
		"ACMECORP_ROOT",
		"acmecorps",
		"acmecorp2",
		"anchor_cli",
		"acmetools_client",
		"acme_tools",
		"AcmeTools",
		"github.com/example/acmecorp-fixtures",
	}
	for _, spelling := range spellings {
		t.Run(spelling, func(t *testing.T) {
			root := writeTree(t, map[string]string{"p.go": "package p\n\n// " + spelling + "\n"})
			hits := scan(t, root, invented)
			if len(hits) == 0 {
				t.Fatalf("%s reported nothing: a name spelled inside an identifier is still the name", spelling)
			}
			if hits[0].Line != 3 {
				t.Errorf("hit = %+v, want line 3", hits[0])
			}
		})
	}
}

// The one restriction, and it is narrow: a name that is also an ordinary
// English word matches only where it is not part of a longer word. Everything
// here would be a false report, and false reports are how a check gets ignored.
func TestScanLeavesOrdinaryWordsAlone(t *testing.T) {
	body := "package p\n\n// unanchored anchorage anchored Anchorman name nothing.\nvar x = 1\n"
	root := writeTree(t, map[string]string{"p.go": body})

	hits := scan(t, root, invented)
	if len(hits) != 0 {
		t.Fatalf("hits = %+v, want none: no word here is a repository name", hits)
	}
}

// A hyphenated derivative is the same repository, so one entry covers the
// family rather than needing one entry per spelling.
func TestScanCatchesHyphenatedDerivatives(t *testing.T) {
	root := writeTree(t, map[string]string{
		"p.go": "package p\n\n// anchor-cli and homebrew-anchor are both it.\nvar x = 1\n",
	})

	hits := scan(t, root, invented)
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want both derivatives", hits)
	}
	for _, hit := range hits {
		if hit.Term != "anchor" || hit.Line != 3 {
			t.Errorf("hit = %+v, want the anchor entry on line 3", hit)
		}
	}
}

// A name travels in a path exactly as it travels in a comment, and renaming a
// fixture after the repository it was taken from is the most natural thing a
// refresh does. Each name is reported once, where it is written.
func TestScanCatchesNamesInFileAndDirectoryNames(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/providers/acmecorp_fixture.go": "package providers\n",
		"internal/anchor/reach.go":               "package p\n",
		"testdata/anchor-cli/routes.json":        "{}\n",
	})

	hits := scan(t, root, invented)
	want := []Hit{
		{File: "internal/anchor", Term: "anchor", Text: "anchor"},
		{File: "internal/providers/acmecorp_fixture.go", Term: "acmecorp", Text: "acmecorp_fixture.go"},
		{File: "testdata/anchor-cli", Term: "anchor", Text: "anchor-cli"},
	}
	if len(hits) != len(want) {
		t.Fatalf("hits = %+v, want %+v", hits, want)
	}
	for i, hit := range hits {
		if hit != want[i] {
			t.Errorf("hit %d = %+v, want %+v", i, hit, want[i])
		}
	}
}

// A vendored tree is committed by Go convention and travels in a split exactly
// as the code above it does, so a golden or a fixture parked there carries a
// name out the same way a comment would.
func TestScanReadsVendoredTrees(t *testing.T) {
	root := writeTree(t, map[string]string{
		"vendor/example.com/lib/golden.json": "{\"repo\": \"acmecorp\"}\n",
	})

	hits := scan(t, root, invented)
	if len(hits) != 1 || hits[0].File != "vendor/example.com/lib/golden.json" || hits[0].Line != 1 {
		t.Fatalf("hits = %+v, want the vendored golden", hits)
	}
}

// A snapshot taken inside this tree is this tool's own output about other
// repositories: it names them by design, it is nobody's comment, and its fact
// stream runs to tens of megabytes. Reading it would make the check slow and
// permanently red — which is how a check stops being read.
func TestScanSkipsSnapshotsAndGeneratedBulk(t *testing.T) {
	root := writeTree(t, map[string]string{
		".enola/facts.jsonl":               "{\"repo\": \"acmecorp\"}\n",
		".enola-universe/facts.jsonl":      "{\"repo\": \"acmecorp\"}\n",
		"internal/testdata/generated.json": strings.Repeat("x", maxFileSize) + "\nacmecorp\n",
	})

	hits := scan(t, root, invented)
	if len(hits) != 0 {
		t.Fatalf("hits = %+v, want none: generated bulk is not where a name is written", hits)
	}
}

// The denylist lives outside the tree it guards, so a copy of the tree standing
// on its own cannot find it. That is the upstream case, and it is not a
// failure: there is nothing to check.
func TestLoadTermsReportsAnAbsentListAsAState(t *testing.T) {
	_, err := LoadTerms(filepath.Join(t.TempDir(), "estate-terms.txt"))
	if !errors.Is(err, ErrTermsAbsent) {
		t.Fatalf("err = %v, want %v", err, ErrTermsAbsent)
	}
}

func TestLoadTermsReadsNamesAndModifiers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "estate-terms.txt")
	body := "# commentary\n\nacmecorp\nanchor word\nledger except internal/inherited/ except docs/\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	terms, err := LoadTerms(path)
	if err != nil {
		t.Fatalf("LoadTerms: %v", err)
	}
	want := []Term{
		{Text: "acmecorp"},
		{Text: "anchor", Word: true},
		{Text: "ledger", Except: []string{"internal/inherited/", "docs/"}},
	}
	if !reflect.DeepEqual(terms, want) {
		t.Fatalf("terms = %+v, want %+v", terms, want)
	}
}

// A modifier nobody implemented is a rule somebody believes is in force. It
// fails the read rather than being ignored.
func TestLoadTermsRefusesAnUnknownModifier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "estate-terms.txt")
	if err := os.WriteFile(path, []byte("acmecorp wholeword\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTerms(path); err == nil {
		t.Fatal("LoadTerms accepted a modifier it does not implement")
	}
}

func listedTerms(t *testing.T) []Term {
	t.Helper()
	terms, err := LoadTerms(termsPath)
	if errors.Is(err, ErrTermsAbsent) {
		t.Skipf("%v — this tree is not sitting beside the list, so there is nothing to check", err)
	}
	if err != nil {
		t.Fatalf("LoadTerms: %v", err)
	}
	return terms
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func scan(t *testing.T, root string, terms []Term) []Hit {
	t.Helper()
	hits, err := Scan(root, terms)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return hits
}
