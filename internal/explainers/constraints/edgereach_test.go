package constraints

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// The structural defect the previous machinery carried: it ORed three arms
// belonging to different directions, so an edge pointing the wrong way declared
// a component sighted. Both halves are pinned here, in both directions, because
// a mechanism right in one direction and wrong in the other passes any test
// that only walks one.

func concept(name string, extra map[string]any) facts.Fact {
	return predicateComponentIntent(name, map[string]string{"superclass": "StandardError"}, extra)
}

func exceptionClass(name, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file,
		Props: map[string]any{"superclass": "StandardError", "symbol_kind": "class"}}
}

// A source-side role must not be declared sighted by an INBOUND edge. The
// concept is named by a calls edge and sources none; owners: resolves the
// source of every edge, so the rule would read "no edge is owned" and breach
// every arrival at the protected component.
func TestReach_AnInboundEdgeDoesNotSightASourceSideRole(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		concept("exceptions", map[string]any{"owns": intent.OwnsNothing}),
		componentIntent("models", "app/models/**"),
		formRuleIntent("models-are-owned", map[string]any{
			"protect": "models", "owners": "exceptions", "via": "calls"}),
		exceptionClass("TimeoutError", "app/errors/timeout_error.rb"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb",
			Props: map[string]any{"symbol_kind": "class"}},
		// Inbound: something calls the concept. Outbound: the concept's members
		// carry no calls edge at all, which is what owners: has to resolve.
		facts.Fact{Kind: facts.KindSymbol, Name: "Card#render", File: "app/views/card.rb",
			Props:     map[string]any{"symbol_kind": "method"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "TimeoutError"}, {Kind: facts.RelCalls, Target: "Job"}}},
	)
	insights := explain(t, store)
	want := "Constraint rule models-are-owned cannot verdict: exceptions resolves no calls edge in the owners role"
	if got := insightTitled(insights, want); got == nil {
		t.Fatalf("insights = %+v, want %q — an inbound edge is not source reach", insights, want)
	}
	if got := violationTitles(insights); len(got) != 0 {
		t.Fatalf("violations = %v, want none: an owners: that owns no edge would breach every arrival", got)
	}
}

// And the mirror. A target-side role must not be declared sighted by an
// OUTBOUND edge: the concept's members source calls edges and no calls edge
// names them, so to: resolves nothing and a forbidden edge would read as absent.
func TestReach_AnOutboundEdgeDoesNotSightATargetSideRole(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		concept("exceptions", map[string]any{"owns": intent.OwnsNothing}),
		componentIntent("views", "app/views/**"),
		formRuleIntent("views-avoid-exceptions", map[string]any{
			"forbid": "views", "to": "exceptions", "via": "calls"}),
		facts.Fact{Kind: facts.KindSymbol, Name: "TimeoutError", File: "app/errors/timeout_error.rb",
			Props:     map[string]any{"superclass": "StandardError", "symbol_kind": "class"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Logger"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Card#render", File: "app/views/card.rb",
			Props:     map[string]any{"symbol_kind": "method"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Logger"}}},
	)
	insights := explain(t, store)
	want := "Constraint rule views-avoid-exceptions cannot verdict: exceptions resolves no calls edge in the to role"
	if got := insightTitled(insights, want); got == nil {
		t.Fatalf("insights = %+v, want %q — an outbound edge is not target reach", insights, want)
	}
}

// An edge kind the snapshot never measured is not unreachability. Absent and
// unreachable are different claims, and reporting the first as the second is
// how a rule over a language the snapshot does not cover reads as broken.
func TestReach_AnUnmeasuredEdgeKindIsNotUnreachability(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		concept("exceptions", map[string]any{"owns": intent.OwnsNothing}),
		componentIntent("models", "app/models/**"),
		formRuleIntent("models-are-owned", map[string]any{
			"protect": "models", "owners": "exceptions", "via": "calls"}),
		exceptionClass("TimeoutError", "app/errors/timeout_error.rb"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb",
			Props: map[string]any{"symbol_kind": "class"}},
	)
	for _, insight := range explain(t, store) {
		if strings.Contains(insight.Title, "cannot verdict") {
			t.Fatalf("insight = %+v, want none: no fact in this snapshot sources a calls edge at all", insight)
		}
	}
}

// The reach check is asked of a concept, never of a path component: a path
// component's file carriers are its declared reach, and refusing one would
// retire enforcement that has always worked.
func TestReach_APathComponentIsNeverUnreachable(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("errors", "app/errors/**"),
		componentIntent("models", "app/models/**"),
		formRuleIntent("models-are-owned", map[string]any{
			"protect": "models", "owners": "errors", "via": "calls"}),
		facts.Fact{Kind: facts.KindSymbol, Name: "TimeoutError", File: "app/errors/timeout_error.rb",
			Props: map[string]any{"symbol_kind": "class"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb",
			Props: map[string]any{"symbol_kind": "class"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Card#render", File: "app/views/card.rb",
			Props:     map[string]any{"symbol_kind": "method"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Job"}}},
	)
	insights := explain(t, store)
	for _, insight := range insights {
		if strings.Contains(insight.Title, "cannot verdict") {
			t.Fatalf("insight = %+v, want none: a path component is not screened for reach", insight)
		}
	}
	if got := violationTitles(insights); len(got) != 1 {
		t.Fatalf("violations = %v, want the one unowned arrival", got)
	}
}

// A rule walking several edge kinds that reaches a verdict over some of them
// judged the others; refusing it would delete enforcement that worked. The
// silence is reported below the gate instead — but only for a role whose empty
// resolution is no verdict. A breaching role gets no such licence, which the
// owners: case above pins.
func TestReach_PartialSilenceOnAMultiKindRuleIsANoteRatherThanARefusal(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		concept("exceptions", map[string]any{"owns": intent.OwnsNothing, "match": "app/errors/**"}),
		componentIntent("views", "app/views/**"),
		formRuleIntent("views-avoid-exceptions", map[string]any{
			"forbid_reach": "views", "to": "exceptions"}),
		facts.Fact{Kind: facts.KindSymbol, Name: "TimeoutError", File: "app/errors/timeout_error.rb",
			Props: map[string]any{"superclass": "StandardError", "symbol_kind": "class"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Card#render", File: "app/views/card.rb",
			Props: map[string]any{"symbol_kind": "method"},
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "TimeoutError"},
				{Kind: facts.RelImports, Target: "app/errors/other"},
			}},
	)
	insights := explain(t, store)
	for _, insight := range insights {
		if strings.Contains(insight.Title, "cannot verdict") {
			t.Fatalf("insight = %+v, want a note rather than a refusal: the rule judged the kind it could reach", insight)
		}
	}
	note := insightTitled(insights, "Constraint rule views-avoid-exceptions judged no imports edge over exceptions in the to role")
	if note == nil {
		t.Fatalf("insights = %+v, want the partial-silence note", insights)
	}
	if note.Confidence != reachSkipConfidence {
		t.Errorf("confidence = %v, want the below-the-gate %v", note.Confidence, reachSkipConfidence)
	}
	if got := violationTitles(insights); len(got) != 1 {
		t.Fatalf("violations = %v, want the calls reach the rule could judge", got)
	}
}

// Lint and the gate read one measurement, so the authoring loop and the
// enforcement can never disagree about which role resolves nothing.
func TestReach_LintReportsWhatTheGateRefusesOn(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		concept("exceptions", map[string]any{"owns": intent.OwnsNothing}),
		componentIntent("models", "app/models/**"),
		formRuleIntent("models-are-owned", map[string]any{
			"protect": "models", "owners": "exceptions", "via": "calls"}),
		exceptionClass("TimeoutError", "app/errors/timeout_error.rb"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb",
			Props: map[string]any{"symbol_kind": "class"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Card#render", File: "app/views/card.rb",
			Props:     map[string]any{"symbol_kind": "method"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Job"}}},
	)
	got := UnreachableRoles(store)
	if len(got) != 1 || got[0].Component != "exceptions" || got[0].Role != "owners" || got[0].Side != intent.SideSource {
		t.Fatalf("UnreachableRoles = %+v, want the owners role reported on the source side", got)
	}
}
