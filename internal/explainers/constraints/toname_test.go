package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func forbidToNameRuleIntent(id, forbid, toName, via, because string) facts.Fact {
	props := map[string]any{"intent_kind": "rule", "rule": id, "forbid": forbid,
		"to_name": toName, "via": via, "because": because, "source": "wiki/p.md"}
	return facts.Fact{Kind: facts.KindIntent, Name: "rule: " + id, File: "wiki/p.md", Props: props}
}

// The far end of a forbidden edge is often something the snapshot measures no
// fact for: an external package, or a function imported from one whose call
// target resolves to nothing. A component cannot name it, so the whole class of
// rules that forbid reaching OUT of the repository was unwritable. The near end
// is measured either way, which is what makes the verdict honest.
func externalWorld() *facts.Store {
	store := facts.NewStore()
	store.Add(
		componentIntent("components", "app/components/**"),
		forbidToNameRuleIntent("components-avoid-legacy-lifecycle", "components",
			"@ember/render-modifiers*", "imports",
			"A named modifier attaches to the element it acts on."),
		facts.Fact{Kind: facts.KindSymbol, Name: "app/components.OldPanel", File: "app/components/old-panel.gjs",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "@ember/render-modifiers/modifiers/did-insert"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "app/components.CleanPanel", File: "app/components/clean-panel.gjs",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "ember-modifier"}}},
	)
	return store
}

func TestExplain_ForbidToNameMatchesAnUnmeasuredFarEnd(t *testing.T) {
	insights, err := New().Explain(context.Background(), externalWorld())
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	if !strings.Contains(got.Title, "app/components.OldPanel") ||
		!strings.Contains(got.Title, "@ember/render-modifiers/modifiers/did-insert") {
		t.Errorf("title = %q, want the importer and the subpath it imported", got.Title)
	}
	if strings.Contains(got.Title, "CleanPanel") {
		t.Errorf("title = %q: a neighbouring package is not the forbidden one", got.Title)
	}
	// The claim a literal supports is weaker than component membership, and the
	// verdict says so rather than borrowing the stronger wording.
	if !strings.Contains(got.Description, "matches the named literal") {
		t.Errorf("description must state what the match rests on, got: %q", got.Description)
	}
}

// The prefix form is what makes the dialect fit real graphs: a package is
// imported by subpath far more often than by its bare name, and a rule that
// could only spell the bare name would miss every real importer.
func TestExplain_ForbidToNameBareNameDoesNotMatchASubpath(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("components", "app/components/**"),
		forbidToNameRuleIntent("no-subpath", "components", "@ember/render-modifiers", "imports", "x"),
		facts.Fact{Kind: facts.KindSymbol, Name: "app/components.OldPanel", File: "app/components/old-panel.gjs",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "@ember/render-modifiers/modifiers/did-insert"}}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 0 {
		t.Fatalf("insights = %+v, want none: an exact literal is exact, and the prefix form is how a subpath is named", insights)
	}
}
