package constraints

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// predicateComponentIntent compiles one component whose selector is a where
// predicate, through exactly the encoding intent.CompileFacts writes — so these
// tests exercise the same round trip a real declaration takes rather than a
// shape only the test knows how to build.
func predicateComponentIntent(name string, where map[string]string, extra map[string]any) facts.Fact {
	props := map[string]any{
		"intent_kind": "component",
		"component":   name,
		"where":       intent.EncodeWhere(where),
		"source":      "enola/constraints/concepts.yaml",
	}
	for k, v := range extra {
		props[k] = v
	}
	return facts.Fact{Kind: facts.KindIntent, Name: "component: " + name, File: "enola/constraints/concepts.yaml", Props: props}
}

func formRuleIntent(id string, props map[string]any) facts.Fact {
	full := map[string]any{
		"intent_kind": "rule",
		"rule":        id,
		"because":     "the concept is the law's subject, not the directory",
		"source":      "enola/constraints/concepts.yaml",
	}
	for k, v := range props {
		full[k] = v
	}
	return facts.Fact{Kind: facts.KindIntent, Name: "rule: " + id, File: "enola/constraints/concepts.yaml", Props: full}
}

func memberNames(t *testing.T, store *facts.Store, name string) []string {
	t.Helper()
	components, _ := declarations(store)
	c, found := components[name]
	if !found {
		t.Fatalf("component %q is not declared", name)
	}
	names, _ := resolveMembership(store, c)
	return sortedMemberNames(names)
}

func viewComponent(name, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file,
		Props: map[string]any{"superclass": "ViewComponent::Base", "symbol_kind": "class", "framework": "rails"}}
}

func TestWhere_SelectsByWhatFactsCarryNotWhereTheySit(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("view-components", map[string]string{"superclass": "ViewComponent::Base"}, nil),
		viewComponent("Jobs::RelatedJobs", "app/components/jobs/related_jobs.rb"),
		viewComponent("Blocks::Hero", "lib/blocks/hero.rb"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Jobs::RelatedJobs#render", File: "app/components/jobs/related_jobs.rb",
			Props: map[string]any{"symbol_kind": "method", "framework": "rails"}},
	)
	got := memberNames(t, store, "view-components")
	want := []string{"Blocks::Hero", "Jobs::RelatedJobs"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("members = %v, want %v — two directories, one concept, and the method in the same file is not a member", got, want)
	}
}

func TestWhere_IsAConjunctionOfEveryPair(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("ember-presentation", map[string]string{"framework": "ember", "symbol_kind": "class"}, nil),
		facts.Fact{Kind: facts.KindSymbol, Name: "JobCard", File: "ember_app/app/components/job-card.js",
			Props: map[string]any{"framework": "ember", "symbol_kind": "class"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "formatSalary", File: "ember_app/app/utils/format.js",
			Props: map[string]any{"framework": "ember", "symbol_kind": "function"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Invoice", File: "app/models/invoice.rb",
			Props: map[string]any{"framework": "rails", "symbol_kind": "class"}},
	)
	got := memberNames(t, store, "ember-presentation")
	if len(got) != 1 || got[0] != "JobCard" {
		t.Errorf("members = %v, want [JobCard]: a fact satisfying one pair and not the other is not a member", got)
	}
}

// The set props the codebase already carries space-joined (columns,
// fk_constraints, decorators) are matched one whole member at a time, exactly
// as the require form's when_prop_contains reads them — and never by substring,
// so company_id is not satisfied by parent_company_id.
func TestWhere_SetPropsAreWholeMemberContainment(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("tenant-tables", map[string]string{"columns": "company_id"}, map[string]any{"kind": facts.KindStorage}),
		facts.Fact{Kind: facts.KindStorage, Name: "jobs", File: "app/models/job.rb",
			Props: map[string]any{"columns": "id company_id created_at", "storage_kind": "model"}},
		facts.Fact{Kind: facts.KindStorage, Name: "accounts", File: "app/models/account.rb",
			Props: map[string]any{"columns": "id parent_company_id", "storage_kind": "model"}},
		facts.Fact{Kind: facts.KindStorage, Name: "company_id", File: "app/models/odd.rb",
			Props: map[string]any{"columns": "id", "storage_kind": "model"}},
	)
	got := memberNames(t, store, "tenant-tables")
	if len(got) != 1 || got[0] != "jobs" {
		t.Errorf("members = %v, want [jobs]: containment is whole-member, never substring", got)
	}
}

// A scalar prop decomposes into one token, so containment and equality are the
// same statement about it: the value must be the whole prop, not a word of it.
func TestWhere_ScalarPropsAreExactEquality(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("records", map[string]string{"superclass": "ApplicationRecord"}, nil),
		facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb",
			Props: map[string]any{"superclass": "ApplicationRecord"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "LegacyJob", File: "app/models/legacy_job.rb",
			Props: map[string]any{"superclass": "ApplicationRecordLegacy"}},
	)
	got := memberNames(t, store, "records")
	if len(got) != 1 || got[0] != "Job" {
		t.Errorf("members = %v, want [Job]: a prefix of the value is not the value", got)
	}
}

func TestWhere_NumericThresholdsReadTheMeasuredNumber(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("hairy", map[string]string{"cyclomatic": ">=10"}, nil),
		predicateComponentIntent("exactly-ten", map[string]string{"cyclomatic": "10"}, nil),
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing#charge", File: "app/services/billing.rb",
			Props: map[string]any{"cyclomatic": float64(14)}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing#refund", File: "app/services/billing.rb",
			Props: map[string]any{"cyclomatic": 10}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing#name", File: "app/services/billing.rb",
			Props: map[string]any{"cyclomatic": float64(1)}},
	)
	got := memberNames(t, store, "hairy")
	want := []string{"Billing#charge", "Billing#refund"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("threshold members = %v, want %v", got, want)
	}
	if got := memberNames(t, store, "exactly-ten"); len(got) != 1 || got[0] != "Billing#refund" {
		t.Errorf("equality members = %v, want [Billing#refund]: a bare number is equality, and survives the JSON round trip as a float", got)
	}
}

// A component may carry both narrowings, and they intersect: the path scope
// bounds where to look, the predicate bounds what to accept. Every other field
// on a component narrows, so making this one widen would be a second rule for
// no gain — and intersection is what lets a trusted path scope be sharpened
// rather than replaced.
func TestWhere_CombinesWithMatchAsIntersection(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("app-components", map[string]string{"superclass": "ViewComponent::Base"},
			map[string]any{"match": "app/components/**"}),
		viewComponent("Jobs::RelatedJobs", "app/components/jobs/related_jobs.rb"),
		viewComponent("Blocks::Hero", "lib/blocks/hero.rb"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Jobs::RelatedJobs#render", File: "app/components/jobs/related_jobs.rb",
			Props: map[string]any{"symbol_kind": "method"}},
	)
	got := memberNames(t, store, "app-components")
	if len(got) != 1 || got[0] != "Jobs::RelatedJobs" {
		t.Errorf("members = %v, want [Jobs::RelatedJobs]: inside the path AND satisfying the predicate", got)
	}
}

// Carriers are narrowed by the predicate applied to the dependency fact itself,
// never inherited from a member sharing its file: a fact that did not
// demonstrate the property does not carry the component's edges.
func TestWhere_CarriersSatisfyThePredicateThemselves(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("ruby-app", map[string]string{"language": "ruby"}, map[string]any{"match": "app/**"}),
		componentIntent("vendor", "vendor/**"),
		formRuleIntent("app-avoids-vendor", map[string]any{"forbid": "ruby-app", "to": "vendor", "via": "imports"}),
		facts.Fact{Kind: facts.KindDependency, Name: "app -> vendor/legacy", File: "app/services/billing.rb",
			Props:     map[string]any{"language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "vendor/legacy"}}},
		facts.Fact{Kind: facts.KindDependency, Name: "app -> vendor/other", File: "app/javascript/pack.js",
			Props:     map[string]any{"language": "javascript"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "vendor/other"}}},
		facts.Fact{Kind: facts.KindModule, Name: "vendor/legacy", File: "vendor/legacy"},
		facts.Fact{Kind: facts.KindModule, Name: "vendor/other", File: "vendor/other"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, insight := range insights {
		if strings.Contains(insight.Title, "app-avoids-vendor violated") {
			titles = append(titles, insight.Title)
		}
	}
	if len(titles) != 1 || !strings.Contains(titles[0], "vendor/legacy") {
		t.Errorf("verdicts = %v, want exactly the ruby carrier's edge", titles)
	}
}

// The single most important safety property: a where naming a property nothing
// measures is a NAMED problem at full confidence, and the rules over it emit
// nothing rather than passing vacuously. An empty component makes every rule
// hold, which reads exactly like compliance.
func TestWhere_UnmeasuredPropertyIsLoudAndSilencesItsRules(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("models", map[string]string{"superclas": "ApplicationRecord"}, nil),
		formRuleIntent("no-models-at-all", map[string]any{"forbid_fact": "models"}),
		facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb",
			Props: map[string]any{"superclass": "ApplicationRecord"}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %+v, want exactly one: the unmeasured-selector finding, and no vacuous pass", insights)
	}
	got := insights[0]
	want := "Constraint component models selects on unmeasured property superclas"
	if got.Title != want {
		t.Fatalf("title = %q, want %q", got.Title, want)
	}
	if got.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0 — louder than the 0.4 dead-selector advisory, because this one is not ambiguous", got.Confidence)
	}
	if !strings.Contains(got.Description, "superclass") {
		t.Errorf("description must name the near miss, got: %q", got.Description)
	}
	if !strings.Contains(got.Description, "emitted no verdict") {
		t.Errorf("description must say the rules were silenced, got: %q", got.Description)
	}
	unmeasured := UnevaluableSelectors(store)
	if len(unmeasured) != 1 || unmeasured[0].Component != "models" || unmeasured[0].Prop != "superclas" {
		t.Fatalf("UnevaluableSelectors = %+v, want the one lint problem", unmeasured)
	}
	if len(unmeasured[0].NearMiss) == 0 || unmeasured[0].NearMiss[0] != "superclass" {
		t.Errorf("near miss = %v, want superclass first", unmeasured[0].NearMiss)
	}
}

// A measured property whose VALUE matches nothing is the existing dead-selector
// advisory, not the unmeasured-property finding: the two failures are distinct
// and must not collapse into one.
func TestWhere_MeasuredPropertyWithNoMatchingValueStaysTheDeadSelectorAdvisory(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("ghosts", map[string]string{"superclass": "NoSuchBase"}, nil),
		facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb",
			Props: map[string]any{"superclass": "ApplicationRecord"}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 || insights[0].Title != "Constraint component ghosts matches nothing" {
		t.Fatalf("insights = %+v, want the dead-selector advisory", insights)
	}
	if insights[0].Confidence != emptyComponentConfidence {
		t.Errorf("confidence = %v, want %v", insights[0].Confidence, emptyComponentConfidence)
	}
	if !strings.Contains(insights[0].Description, "where superclass=NoSuchBase") {
		t.Errorf("the advisory must render the predicate it resolved, got: %q", insights[0].Description)
	}
	if len(UnevaluableSelectors(store)) != 0 {
		t.Errorf("UnevaluableSelectors = %+v, want none: the property is measured, the value simply matches nothing", UnevaluableSelectors(store))
	}
}

// A store with no measured facts at all reports nothing: "absent" and "never
// looked" must never render the same, the distinction the guidance exemplars
// already draw.
func TestWhere_UnmeasuredPropertyIsSilentWithoutASnapshot(t *testing.T) {
	store := facts.NewStore()
	store.Add(predicateComponentIntent("models", map[string]string{"superclas": "ApplicationRecord"}, nil))
	if got := UnevaluableSelectors(store); len(got) != 0 {
		t.Errorf("UnevaluableSelectors = %+v, want none with nothing measured", got)
	}
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if strings.Contains(insight.Title, "unmeasured property") {
			t.Errorf("insight = %q, want no unmeasured-property claim from a declarations-only store", insight.Title)
		}
	}
}

// MemberCounts is what `constraints lint` prints, and a predicate count with no
// predicate beside it leaves the author guessing which narrowing produced it.
func TestWhere_LintCountsAndRendersThePredicate(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("view-components", map[string]string{"superclass": "ViewComponent::Base"}, nil),
		predicateComponentIntent("rails-models", map[string]string{"storage_kind": "model", "framework": "rails"},
			map[string]any{"kind": facts.KindStorage}),
		viewComponent("Jobs::RelatedJobs", "app/components/jobs/related_jobs.rb"),
		viewComponent("Blocks::Hero", "lib/blocks/hero.rb"),
		facts.Fact{Kind: facts.KindStorage, Name: "jobs", File: "app/models/job.rb",
			Props: map[string]any{"storage_kind": "model", "framework": "rails"}},
	)
	got := MemberCounts(store)
	want := []ComponentCount{
		{Component: "rails-models", Members: 1, Selector: "kind storage, where framework=rails, where storage_kind=model"},
		{Component: "view-components", Members: 2, Selector: "where superclass=ViewComponent::Base"},
	}
	if len(got) != len(want) {
		t.Fatalf("MemberCounts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MemberCounts[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Every rule form must verdict over a predicate-selected component exactly as
// it does over a path-selected one: the selector is the only thing that
// changed, so a form that reads membership differently would be a second
// vocabulary. One breach per form, all twelve law forms plus guidance.
func TestWhere_EveryRuleFormVerdictsOverAPredicateComponent(t *testing.T) {
	base := func() *facts.Store {
		store := facts.NewStore()
		store.Add(
			predicateComponentIntent("presentation", map[string]string{"superclass": "ViewComponent::Base"}, nil),
			predicateComponentIntent("records", map[string]string{"superclass": "ApplicationRecord"}, nil),
			facts.Fact{Kind: facts.KindSymbol, Name: "Jobs::Card", File: "app/components/jobs/card.rb",
				Props: map[string]any{"superclass": "ViewComponent::Base", "symbol_kind": "class", "exported": false},
				Relations: []facts.Relation{
					{Kind: facts.RelCalls, Target: "Job"},
					{Kind: facts.RelImplements, Target: "ViewComponent::Base"},
				}},
			facts.Fact{Kind: facts.KindSymbol, Name: "Job", File: "app/models/job.rb",
				Props: map[string]any{"superclass": "ApplicationRecord", "symbol_kind": "class", "exported": true},
				Relations: []facts.Relation{
					{Kind: facts.RelCalls, Target: "Jobs::Card"},
				}},
		)
		return store
	}
	cases := map[string]struct {
		rule facts.Fact
		want string
	}{
		"forbid": {
			rule: formRuleIntent("presentation-avoids-records", map[string]any{"forbid": "presentation", "to": "records", "via": "calls"}),
			want: "Constraint presentation-avoids-records violated: Jobs::Card -> Job via calls",
		},
		"forbid_reach": {
			rule: formRuleIntent("presentation-never-reaches-records", map[string]any{"forbid_reach": "presentation", "to": "records"}),
			want: "Constraint presentation-never-reaches-records violated: Jobs::Card reaches Job",
		},
		"allow": {
			rule: formRuleIntent("presentation-lands-in-presentation", map[string]any{"allow": "presentation", "only": "presentation", "via": "calls"}),
			want: "Constraint presentation-lands-in-presentation violated: Jobs::Card -> Job via calls",
		},
		"protect": {
			rule: formRuleIntent("records-are-owned", map[string]any{"protect": "records", "owners": "records", "via": "calls"}),
			want: "Constraint records-are-owned violated: Jobs::Card -> Job via calls",
		},
		"private": {
			rule: formRuleIntent("presentation-is-private", map[string]any{"private": "presentation"}),
			want: "Constraint presentation-is-private violated: Job -> Jobs::Card",
		},
		"forbid_fact": {
			rule: formRuleIntent("no-presentation", map[string]any{"forbid_fact": "presentation"}),
			want: "Constraint no-presentation violated: Jobs::Card is measured in presentation",
		},
		"cap": {
			rule: formRuleIntent("presentation-is-bounded", map[string]any{"cap": "presentation", "max_members": 0}),
			want: "Constraint presentation-is-bounded violated",
		},
		"require": {
			rule: formRuleIntent("presentation-declares-a-framework", map[string]any{
				"require": "presentation", "must_prop": "framework", "must_value": "rails"}),
			want: "Constraint presentation-declares-a-framework violated: Jobs::Card must have framework containing rails",
		},
		"require_defines": {
			rule: formRuleIntent("records-define-to-h", map[string]any{"require_defines": "records", "method": "to_h"}),
			want: "Constraint records-define-to-h violated: Job does not define to_h",
		},
		"require_name": {
			rule: formRuleIntent("presentation-is-suffixed", map[string]any{"require_name": "presentation", "pattern": "*Component"}),
			want: "Constraint presentation-is-suffixed violated: Jobs::Card does not match *Component",
		},
		"forbid_name": {
			rule: formRuleIntent("presentation-avoids-card-names", map[string]any{"forbid_name": "presentation", "pattern": "*Card"}),
			want: "Constraint presentation-avoids-card-names violated: Jobs::Card matches the forbidden *Card",
		},
		"require_edge": {
			rule: formRuleIntent("presentation-is-rendered", map[string]any{
				"require_edge": "presentation", "via": "implements", "direction": "inbound"}),
			want: "Constraint presentation-is-rendered violated: Jobs::Card has no inbound implements edge",
		},
		"protocol": {
			rule: formRuleIntent("presentation-follows-order", map[string]any{
				"protocol": "presentation", "steps": "presentation records", "via": "calls"}),
			want: "Constraint presentation-follows-order violated: Jobs::Card calls records without presentation",
		},
		"guide": {
			rule: formRuleIntent("presentation-advice", map[string]any{
				"guide": "presentation", "message": "prefer a slot over a model lookup", "mode": "advisory"}),
			want: "Guidance for presentation: presentation-advice",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := base()
			store.Add(tc.rule)
			insights, err := New().Explain(context.Background(), store)
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			for _, insight := range insights {
				titles = append(titles, insight.Title)
			}
			sort.Strings(titles)
			for _, title := range titles {
				if strings.HasPrefix(title, tc.want) {
					return
				}
			}
			t.Errorf("titles = %v, want one starting %q — every form must verdict over a predicate component", titles, tc.want)
		})
	}
}

// An exemption is matched against the violation identity the rule titles with,
// which the selector does not touch — so a carve-out over a predicate component
// lands in the Exempted bucket exactly as it does over a path one.
func TestWhere_ExemptionsRideAPredicateComponentUnchanged(t *testing.T) {
	store := facts.NewStore()
	rule := formRuleIntent("exceptions-are-named", map[string]any{
		"require_name": "exceptions",
		"pattern":      "*Error",
		"exempt": intent.EncodeExemptions([]intent.ConstraintExemption{{
			Witness: "Sessions::TwoAuthException does not match *Error",
			Owner:   "platform",
			Because: "renaming it is a caller-side change scheduled separately",
			Since:   "2026-08-12",
		}}),
	})
	store.Add(
		predicateComponentIntent("exceptions", map[string]string{"superclass": "StandardError"}, nil),
		rule,
		facts.Fact{Kind: facts.KindSymbol, Name: "Sessions::TwoAuthException", File: "app/services/sessions.rb",
			Props: map[string]any{"superclass": "StandardError", "symbol_kind": "class"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Channel::InvalidCustomPrice", File: "app/models/channel.rb",
			Props: map[string]any{"superclass": "StandardError", "symbol_kind": "class"}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var exempted, violated int
	for _, insight := range insights {
		switch {
		case strings.HasPrefix(insight.Title, "Exempted from constraint exceptions-are-named:"):
			exempted++
		case strings.Contains(insight.Title, "exceptions-are-named violated"):
			violated++
		}
	}
	if exempted != 1 || violated != 1 {
		t.Errorf("exempted = %d, violated = %d, want 1 and 1 — the carve-out lands, the other breach stands", exempted, violated)
	}
}

// The pre-edit contract answers for a predicate component only where the
// snapshot already carries a qualifying fact: a path arm exists so a file that
// does not exist yet still gets its contract, and that is exactly what a
// predicate cannot answer for.
func TestWhere_ContractAnswersOnlyForMeasuredFiles(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("presentation", map[string]string{"superclass": "ViewComponent::Base"},
			map[string]any{"match": "app/components/**"}),
		formRuleIntent("presentation-is-suffixed", map[string]any{"require_name": "presentation", "pattern": "*Component"}),
		viewComponent("Jobs::Card", "app/components/jobs/card.rb"),
	)
	if bindings, _ := ContractFor(store, "app/components/jobs/card.rb"); len(bindings) != 1 {
		t.Errorf("bindings for a measured file = %+v, want the component", bindings)
	}
	if bindings, _ := ContractFor(store, "app/components/jobs/new_card.rb"); len(bindings) != 0 {
		t.Errorf("bindings for an unwritten file = %+v, want none: a predicate cannot answer for what was never measured", bindings)
	}
	if bindings, _ := ContractFor(store, "Jobs::Card"); len(bindings) != 1 {
		t.Errorf("bindings for the fact name = %+v, want the component", bindings)
	}
}
