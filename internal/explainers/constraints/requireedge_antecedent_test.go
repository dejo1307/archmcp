package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The pairing this exercises is two POSITIVE edges: a member that makes one
// edge must also make another. It is what lets a rule say "a getter that reads
// an async relationship must resolve it" without asking the graph for the
// absence of a fact it may never have measured — the antecedent selects on a
// measured edge, and only the selected members are asked for the second.
func relationshipWorld(whenEdgeTo string) *facts.Store {
	rule := requireEdgeRuleIntent("async-relationships-are-resolved", "readers", "helpers",
		"calls", "outbound", "A promise rendered is not the records.")
	if whenEdgeTo != "" {
		rule.Props["when_edge_to"] = whenEdgeTo
		rule.Props["when_via"] = "calls"
	}
	store := facts.NewStore()
	store.Add(
		componentIntent("readers", "app/components/**"),
		componentIntent("helpers", "app/utils/**"),
		rule,
		facts.Fact{Kind: facts.KindSymbol, Name: "app/utils.unwrapPromise", File: "app/utils/promise.ts"},
		facts.Fact{Kind: facts.KindSymbol, Name: "app/models.relationshipFor", File: "app/models/relationship.ts"},
		// Reaches for a relationship and stops there — the breach.
		facts.Fact{Kind: facts.KindSymbol, Name: "app/components.TeamCard.members", File: "app/components/team-card.ts",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "app/models.relationshipFor"}}},
		// Reaches for one and resolves it — compliant.
		facts.Fact{Kind: facts.KindSymbol, Name: "app/components.ResolvedCard.members", File: "app/components/resolved-card.ts",
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "app/models.relationshipFor"},
				{Kind: facts.RelCalls, Target: "app/utils.unwrapPromise"},
			}},
		// Touches no relationship at all, so the rule has nothing to say about
		// it. Without the antecedent it is a false verdict, which is the whole
		// reason the antecedent is here.
		facts.Fact{Kind: facts.KindSymbol, Name: "app/components.ProxyReader.title", File: "app/components/proxy-reader.ts",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "app/models.somethingElse"}}},
	)
	return store
}

func TestExplain_RequireEdgeAntecedentAsksOnlyTheMembersThatMakeTheFirstEdge(t *testing.T) {
	insights, err := New().Explain(context.Background(), relationshipWorld("*.relationshipFor"))
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	if !strings.Contains(got.Title, "app/components.TeamCard.members") {
		t.Errorf("title = %q, want the reader that never resolved what it read", got.Title)
	}
	for _, quiet := range []string{"ResolvedCard", "ProxyReader"} {
		if strings.Contains(got.Title, quiet) {
			t.Errorf("title = %q must not name %s", got.Title, quiet)
		}
	}
	if !strings.Contains(got.Description, "that makes a calls edge to *.relationshipFor") {
		t.Errorf("the verdict must state why this member was asked, got: %q", got.Description)
	}
}

// Without the antecedent every member is asked, and the two that never touched
// a relationship are condemned for not resolving one. The contrast is the
// evidence that the antecedent narrows rather than decorates.
func TestExplain_RequireEdgeWithoutTheAntecedentAsksEveryMember(t *testing.T) {
	insights, err := New().Explain(context.Background(), relationshipWorld(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 2 {
		t.Fatalf("insights = %d, want 2 — TeamCard and ProxyReader: %+v", len(insights), insights)
	}
}

// Nobody selected is the one way this pairing can be vacuous, and it is silent
// by construction: zero selected members is zero violations. The advisory is
// what stops that reading as a clean report.
func TestExplain_RequireEdgeAntecedentSelectingNobodyIsAnAdvisoryNotAPass(t *testing.T) {
	insights, err := New().Explain(context.Background(), relationshipWorld("*.neverCalledByAnyone"))
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly the advisory: %+v", len(insights), insights)
	}
	want := "require_edge rule async-relationships-are-resolved skipped: no member of readers makes a calls edge the antecedent selects"
	if insights[0].Title != want {
		t.Errorf("title = %q, want %q", insights[0].Title, want)
	}
}
