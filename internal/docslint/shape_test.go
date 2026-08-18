package docslint

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const extractionDir = "docs/extraction"

// limitsHeading is the section docs/extraction/README.md argues hardest for: "That
// last section is not an apology. A missing edge shows up in `enola coverage` as an
// unresolved count you can go and look at; a *wrong* edge is invisible and gets acted
// on." A page without it is a page that documents only the capability.
const limitsHeading = "What is deliberately not extracted"

// glanceHeading is the summary table each page is supposed to open with.
const glanceHeading = "At a glance"

// pagesWithoutAGlanceTable records extraction pages that open straight into their
// constructs instead of the summary table docs/extraction/README.md tells readers to
// expect. It is empty, and the test fails if a listed page HAS the section — so the
// list can only shrink, and a new page cannot quietly join it.
//
// It held four entries when this check was written. Each was closed by writing the
// table from the page's own golden facts rather than by widening the rule.
var pagesWithoutAGlanceTable = map[string]string{}

// TestExtractionPagesHaveTheSectionsTheirIndexPromises checks the per-language pages
// against the shape docs/extraction/README.md tells readers to expect.
//
// It is the cheapest form of "everything is represented where it needs to be": not
// whether a page is right, but whether it has the part that is easy to leave out.
// asyncapi.md had the limits content and no heading over it — present to a reader who
// got that far, invisible to anyone scanning, and unassertable.
func TestExtractionPagesHaveTheSectionsTheirIndexPromises(t *testing.T) {
	for _, d := range extractionPages(t) {
		name := path.Base(d.Path)
		a := d.Anchors()

		if !a[Slug(limitsHeading)] {
			t.Errorf("%s has no %q section — docs/extraction/README.md tells readers "+
				"every page states its limits next to its capability", d.Path, limitsHeading)
		}

		hasGlance := a[Slug(glanceHeading)]
		why, pending := pagesWithoutAGlanceTable[name]
		switch {
		case hasGlance && pending:
			t.Errorf("%s now has an %q section, but pagesWithoutAGlanceTable still "+
				"excuses it (%q) — drop the entry", d.Path, glanceHeading, why)
		case !hasGlance && !pending:
			t.Errorf("%s has no %q section — add one, or record why not in "+
				"pagesWithoutAGlanceTable", d.Path, glanceHeading)
		}
	}
}

var indexRowRe = regexp.MustCompile(`\[[^\]]+\]\(([a-z0-9-]+\.md)\)`)

// TestExtractionIndexListsEveryPage keeps the index and the directory in step, both
// ways. A page absent from the table is one no reader will find; a row pointing at a
// page that no longer exists is caught by TestLinksResolve, and this is its converse.
func TestExtractionIndexListsEveryPage(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot, extractionDir, "README.md"))
	if err != nil {
		t.Fatalf("reading the extraction index: %v", err)
	}
	listed := map[string]bool{}
	for _, m := range indexRowRe.FindAllStringSubmatch(string(body), -1) {
		listed[m[1]] = true
	}
	for _, d := range extractionPages(t) {
		if name := path.Base(d.Path); !listed[name] {
			t.Errorf("%s exists but docs/extraction/README.md never links to it", d.Path)
		}
	}
}

func extractionPages(t *testing.T) []Doc {
	t.Helper()
	var out []Doc
	for _, d := range corpus(t) {
		if strings.HasPrefix(d.Path, extractionDir+"/") && path.Base(d.Path) != "README.md" {
			out = append(out, d)
		}
	}
	if len(out) < 15 {
		t.Fatalf("found %d extraction pages, expected one per language", len(out))
	}
	return out
}
