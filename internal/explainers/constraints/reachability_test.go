package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func insightTitled(insights []facts.Insight, title string) *facts.Insight {
	for i := range insights {
		if insights[i].Title == title {
			return &insights[i]
		}
	}
	return nil
}

func explain(t *testing.T, store *facts.Store) []facts.Insight {
	t.Helper()
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	return insights
}

// The central limit of the reduced vocabulary, stated as a test rather than left
// to be discovered. Every imports edge rides a dependency fact, and a dependency
// fact carries none of the properties a concept is selected by — so a predicate
// component's file carries edges the component does not. This is the membership
// half of that statement, which is all the explainer is now asked: the
// declaration that would pair the two is refused before it compiles, by
// intent.predicateRoleProblems, and TestForms_EveryEdgeRoleRefusesAPredicate is
// where that lives.
func TestReach_PredicateComponentDoesNotCarryTheImportsOfItsFile(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("view-components", map[string]string{"superclass": "ViewComponent::Base"}, nil),
		formRuleIntent("no-view-components-are-capped", map[string]any{"cap": "view-components", "max_members": 10}),
		viewComponent("Hires::Cover", "app/components/hires/cover.rb"),
		facts.Fact{Kind: facts.KindModule, Name: "app/models/job", File: "app/models/job.rb"},
		facts.Fact{Kind: facts.KindDependency, Name: "app/components -> app/models/job", File: "app/components/hires/cover.rb",
			Props:     map[string]any{"language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "app/models/job"}}},
	)
	if names := memberNames(t, store, "view-components"); len(names) != 1 || names[0] != "Hires::Cover" {
		t.Fatalf("membership = %v, want only the class that carries the predicate — the dependency fact sharing its file never demonstrated one", names)
	}
	if breaches := violationTitles(explain(t, store)); len(breaches) != 0 {
		t.Fatalf("violations = %v, want none", breaches)
	}
}

func classFact(name, file, superclass string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file,
		Props:     map[string]any{"symbol_kind": "class", "superclass": superclass},
		Relations: []facts.Relation{{Kind: facts.RelImplements, Target: superclass}}}
}

// A one-level superclass selector that leaves measured subclasses outside
// itself is neither empty nor unmeasured, so no existing advisory could see it.
func TestSuperclass_OneLevelSelectorWithSubclassesOutsideItIsReported(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("direct", map[string]string{"superclass": "ViewComponent::Base"}, nil),
		classFact("Blocks::Base", "app/components/blocks/base.rb", "ViewComponent::Base"),
		classFact("Blocks::Text", "app/components/blocks/text.rb", "Blocks::Base"),
	)
	insights := explain(t, store)
	want := "Constraint component direct selects one inheritance level and 1 measured subclass(es) fall outside it"
	got := insightTitled(insights, want)
	if got == nil {
		t.Fatalf("insights = %+v, want %q", insights, want)
	}
	if !strings.Contains(got.Description, "Blocks::Text") {
		t.Errorf("the advisory must name the subclass it measured outside: %q", got.Description)
	}
	// The advisory states a limit; it must not prescribe a spelling this
	// vocabulary does not have.
	if strings.Contains(got.Description, "inherits:") {
		t.Errorf("the advisory names a key that does not exist: %q", got.Description)
	}
	for _, action := range got.Actions {
		if strings.Contains(action, "inherits:") {
			t.Errorf("action names a key that does not exist: %q", action)
		}
	}
}

// The advisory used to call its count "a floor and never a ceiling", which is
// false in exactly the over-attribution direction: childrenOf is keyed on the
// parent AS WRITTEN and read by a member's RESOLVED fact name, so a
// module-scoped Base and a top-level Base are one key and a class inheriting
// the first is named as a subclass of the second. The count is lexical and
// bounded in neither direction, and the sentence has to say so — the keying is
// a property of what `superclass` measures, which is source text.
func TestSuperclass_TheCountIsNamedLexicalRatherThanAFloor(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("direct", map[string]string{"superclass": "ViewComponent::Base"}, nil),
		classFact("Base", "app/components/base.rb", "ViewComponent::Base"),
		// Written `class Card < Base` inside module Widgets: the same text, a
		// different class, and the index cannot tell them apart.
		classFact("Widgets::Card", "app/widgets/card.rb", "Base"),
	)
	got := insightTitled(explain(t, store), "Constraint component direct selects one inheritance level and 1 measured subclass(es) fall outside it")
	if got == nil {
		t.Fatal("want the one-level advisory")
	}
	if strings.Contains(got.Description, "a floor") && !strings.Contains(got.Description, "neither a floor nor a ceiling") {
		t.Errorf("the advisory claims a bound it does not have: %q", got.Description)
	}
	if !strings.Contains(got.Description, "neither a floor nor a ceiling") || !strings.Contains(got.Description, "over-attributes") {
		t.Errorf("the advisory must name both directions it errs in, got: %q", got.Description)
	}
}

// Nothing outside the component means nothing to report: the selector reached
// every class that named its members as a parent.
func TestSuperclass_NoAdvisoryWhenNoSubclassFallsOutside(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("direct", map[string]string{"superclass": "ViewComponent::Base"}, nil),
		classFact("Blocks::Base", "app/components/blocks/base.rb", "ViewComponent::Base"),
		classFact("Blocks::Text", "app/components/blocks/text.rb", "ViewComponent::Base"),
	)
	for _, in := range explain(t, store) {
		if strings.Contains(in.Title, "selects one inheritance level") {
			t.Fatalf("insights = %+v, want no one-level advisory: both classes are members", in)
		}
	}
}

// The counterparty rule outranks the property census. A component naming an
// absent service is unasked, and reporting it as a 1.0 unmeasured-property
// finding replaces "the repo was not loaded" with a claim about measurement
// that this snapshot has no standing to make.
func TestCensus_AbsentServiceStaysTheAbsentServiceAdvisory(t *testing.T) {
	store := facts.NewStore()
	component := predicateComponentIntent("billing-jobs", map[string]string{"queue_adapter": "sidekiq"}, map[string]any{"service": "billing"})
	store.Add(
		component,
		formRuleIntent("billing-jobs-are-capped", map[string]any{"cap": "billing-jobs", "max_members": 10}),
		facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb", Repo: "crm",
			Props: map[string]any{"symbol_kind": "class"}},
	)
	insights := explain(t, store)
	if got := insightTitled(insights, "Constraint component billing-jobs names service billing not present in this snapshot"); got == nil {
		t.Fatalf("insights = %+v, want the 0.4 absent-service advisory", insights)
	} else if got.Confidence != absentServiceConfidence {
		t.Errorf("confidence = %v, want %v", got.Confidence, absentServiceConfidence)
	}
	for _, in := range insights {
		if strings.Contains(in.Title, "unmeasured property") {
			t.Fatalf("insights = %+v, want no unmeasured-property claim about a repo this snapshot does not contain", in)
		}
	}
	if got := UnevaluableSelectors(store); len(got) != 0 {
		t.Errorf("UnevaluableSelectors = %+v, want none — lint and the gate must silence the same component for the same reason", got)
	}
}

// The census is scoped to the component's own service. Over the union it
// answered with the WRONG repo's measurements: a selector its own service
// cannot evaluate looked fine because a sibling repo carried the property, and
// the same config produced 1.00 in one output directory and 0.40 in another.
func TestCensus_IsScopedToTheComponentsService(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("billing-models", map[string]string{"storage_kind": "model"}, map[string]any{"service": "billing"}),
		predicateComponentIntent("crm-models", map[string]string{"storage_kind": "model"}, map[string]any{"service": "crm"}),
		facts.Fact{Kind: facts.KindStorage, Name: "billing/User", File: "billing/app/models/user.rb", Repo: "billing",
			Props: map[string]any{"storage_kind": "model"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "crm/Job", File: "crm/app/models/job.rb", Repo: "crm",
			Props: map[string]any{"symbol_kind": "class"}},
	)
	got := UnevaluableSelectors(store)
	if len(got) != 1 || got[0].Component != "crm-models" || got[0].Prop != "storage_kind" {
		t.Fatalf("UnevaluableSelectors = %+v, want exactly the crm-scoped selector: crm measures no storage_kind, so within its own service the predicate answers nothing", got)
	}
	if names := memberNames(t, store, "billing-models"); len(names) != 1 || names[0] != "billing/User" {
		t.Errorf("membership = %v, want the one billing model — billing's selector is evaluable and unaffected", names)
	}
}

// A threshold against a property no measured fact carries as a number can never
// hold, and validation cannot see it — only the snapshot can.
func TestCensus_ThresholdAgainstANonNumericPropertyIsUnevaluable(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("deep-hierarchies", map[string]string{"superclass": ">=5"}, nil),
		formRuleIntent("no-deep-hierarchies", map[string]any{"forbid_fact": "deep-hierarchies"}),
		classFact("Job", "app/models/job.rb", "ApplicationRecord"),
	)
	insights := explain(t, store)
	want := "Constraint component deep-hierarchies compares non-numeric property superclass against a threshold"
	got := insightTitled(insights, want)
	if got == nil {
		t.Fatalf("insights = %+v, want %q", insights, want)
	}
	if got.Confidence != unmeasuredPropConfidence {
		t.Errorf("confidence = %v, want the fail-closed %v", got.Confidence, unmeasuredPropConfidence)
	}
	if breaches := violationTitles(insights); len(breaches) != 0 {
		t.Errorf("violations = %v, want none: a rule over an unevaluable selector emits no verdict", breaches)
	}
}

// A compiled where prop that does not decode into a property test must select
// nothing and say so — never decode to "no predicate", which would hand the
// component every fact its match patterns cover.
func TestCensus_UndecodablePredicateSelectsNothingAndIsReported(t *testing.T) {
	store := facts.NewStore()
	corrupt := predicateComponentIntent("concepts", nil, map[string]any{"match": "app/**", "where": "superclass= ViewComponent::Base"})
	store.Add(
		corrupt,
		classFact("Job", "app/jobs/job.rb", "ApplicationJob"),
	)
	if got := memberNames(t, store, "concepts"); len(got) != 0 {
		t.Fatalf("membership = %v, want none: a predicate that lost its test must not widen to the whole match", got)
	}
	insights := explain(t, store)
	if insightTitled(insights, "Constraint component concepts compiled to a predicate that is not a property test") == nil {
		t.Fatalf("insights = %+v, want the undecodable-predicate finding", insights)
	}
}
