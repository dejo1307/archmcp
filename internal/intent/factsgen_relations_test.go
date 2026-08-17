package intent

import "testing"

// The knowledge layer's edges were emitted as facts carrying props and never as
// graph relations, so every traversal in the system was blind to them: traverse,
// find_path, impact_analysis and crossrepo all walk Relations, and none could
// step from a governing page to the code it governs. On our knowledge
// repository that is 1,828 anchors and 451 relations — 2,279 edges invisible to
// every walker — while
// `govern` worked only because it reimplements the join by hand.
//
// The props stay. They are what `govern` and the intentcheck explainer already
// read, and removing them would trade one broken consumer for two.
func TestAnAnchorCarriesAGraphRelation(t *testing.T) {
	page := &PageIntent{Page: &PageDecl{Type: "decision",
		Anchors: []PageAnchor{{Repo: "monolith", Path: "app/models/company.rb"}}}}
	for _, f := range CompilePageFacts(page, "wiki/p.md") {
		if f.PropString("intent_kind") != "anchor" {
			continue
		}
		if len(f.Relations) == 0 {
			t.Fatalf("anchor fact carries no relation: %+v", f.Props)
		}
		// Repo-qualified: a bare path would bind to whichever repository in a
		// union happened to have a file by that name.
		if got := f.Relations[0].Target; got != "monolith/app/models/company.rb" {
			t.Fatalf("relation target = %q, want the repo-qualified path", got)
		}
		return
	}
	t.Fatal("no anchor fact was emitted")
}

func TestAPageRelationCarriesAGraphRelation(t *testing.T) {
	page := &PageIntent{Page: &PageDecl{Type: "decision",
		Relations: []PageRelation{{Rel: "depends-on", To: "wiki/x.md"}}}}
	for _, f := range CompilePageFacts(page, "wiki/p.md") {
		if f.PropString("intent_kind") != "relation" {
			continue
		}
		if len(f.Relations) == 0 || f.Relations[0].Target != "wiki/x.md" {
			t.Fatalf("relation fact carries no usable relation: %+v", f)
		}
		return
	}
	t.Fatal("no relation fact was emitted")
}
