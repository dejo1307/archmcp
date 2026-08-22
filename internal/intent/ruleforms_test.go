package intent

import (
	"strings"
	"testing"
)

// The enumeration. A component is party to a rule in one role per form plus the
// counterpart roles, and the previous two rounds each fixed the roles someone
// thought of and left the rest silently wrong. This test is driven by RuleForms
// and CounterpartRoles rather than by a list written here, so a form added to
// the schema without a fixture fails LOUDLY instead of slipping past the screen.

// formFixture builds one syntactically complete rule of a form. roles lists
// every role the form fills with a component, its own form key first; build
// binds each of them through the caller's function so the test can put the
// predicate component in one role at a time and a path component in the rest.
type formFixture struct {
	roles []string
	build func(bind func(role string) string) ConstraintRule
}

func base(r ConstraintRule) ConstraintRule {
	r.ID = "r"
	r.Because = "a rule with no rationale surfaces none"
	return r
}

var formFixtures = map[string]formFixture{
	"forbid": {
		roles: []string{"forbid", "to"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Forbid: bind("forbid"), To: bind("to"), Via: "calls"})
		},
	},
	"forbid_reach": {
		roles: []string{"forbid_reach", "to"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{ForbidReach: bind("forbid_reach"), To: bind("to"), Via: "calls"})
		},
	},
	"allow": {
		roles: []string{"allow", "only"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Allow: bind("allow"), Only: []string{bind("only")}, Via: "calls"})
		},
	},
	"protect": {
		roles: []string{"protect", "owners"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Protect: bind("protect"), Owners: []string{bind("owners")}, Via: "calls"})
		},
	},
	"private": {
		roles: []string{"private", "except"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Private: bind("private"), Except: []string{bind("except")}})
		},
	},
	"require_edge": {
		roles: []string{"require_edge", "to"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{RequireEdge: bind("require_edge"), To: bind("to"), Via: "calls", Direction: "inbound"})
		},
	},
	"protocol": {
		roles: []string{"protocol", "steps"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Protocol: bind("protocol"), Steps: []string{bind("steps"), secondStep}, Via: "calls"})
		},
	},
	"forbid_fact": {
		roles: []string{"forbid_fact"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{ForbidFact: bind("forbid_fact")})
		},
	},
	"cap": {
		roles: []string{"cap"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Cap: bind("cap"), MaxMembers: 10})
		},
	},
	"require": {
		roles: []string{"require"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Require: bind("require"), MustPropContain: &PropMatch{Prop: "columns", Value: "company_id"}})
		},
	},
	"require_defines": {
		roles: []string{"require_defines"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{RequireDefines: bind("require_defines"), Method: "perform"})
		},
	},
	"forbid_cycles": {
		roles: []string{"forbid_cycles"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{ForbidCycles: bind("forbid_cycles"), Among: []string{secondStep}})
		},
	},
	"independent": {
		roles: []string{"independent"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Independent: bind("independent")})
		},
	},
	"require_name": {
		roles: []string{"require_name"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{RequireName: bind("require_name"), Pattern: "*Event"})
		},
	},
	"forbid_name": {
		roles: []string{"forbid_name"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{ForbidName: bind("forbid_name"), Pattern: "*Event"})
		},
	},
	"guide": {
		roles: []string{"guide"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{Guide: bind("guide"), Message: "prior art here used the adapter"})
		},
	},
	"storage_stays_home": {
		roles: []string{"storage_stays_home"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{StorageStaysHome: bind("storage_stays_home")})
		},
	},
	"cap_runtime": {
		roles: []string{"cap_runtime"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{CapRuntime: bind("cap_runtime"), Metric: "queries", Max: 20})
		},
	},
	"require_consumer": {
		roles: []string{"require_consumer"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{RequireConsumer: bind("require_consumer")})
		},
	},
	"unique_across": {
		roles: []string{"unique_across"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{UniqueAcross: bind("unique_across"), By: "table"})
		},
	},
	"require_governed": {
		roles: []string{"require_governed"},
		build: func(bind func(string) string) ConstraintRule {
			return base(ConstraintRule{RequireGoverned: bind("require_governed")})
		},
	},
}

const (
	predicateComponent = "concept"
	pathComponent      = "paths"
	secondStep         = "later"
)

func enumerationComponents() []ConstraintComponent {
	return []ConstraintComponent{
		{Name: predicateComponent, Where: map[string]any{"superclass": "ViewComponent::Base"}},
		{Name: pathComponent, Match: []string{"app/**"}},
		{Name: secondStep, Match: []string{"lib/**"}},
	}
}

// walksEdges answers, for one (form, role) pair, whether the rule resolves that
// role against a measured edge — read off the same two tables the screen reads,
// never off a list written here.
func walksEdges(t *testing.T, formKey, role string) bool {
	t.Helper()
	for _, counterpart := range CounterpartRoles {
		if counterpart.Key == role {
			return true
		}
	}
	for _, form := range RuleForms {
		if form.Key == role {
			if role != formKey {
				t.Fatalf("fixture for %s names role %q, which is another form's subject key", formKey, role)
			}
			return form.WalksEdges
		}
	}
	t.Fatalf("fixture for %s names role %q, which is neither a form key nor a counterpart role", formKey, role)
	return false
}

// TestForms_EveryEdgeRoleScreensAConcept is the totality claim, and the
// declaration half of the matrix: for every form the schema declares and every
// role a component can occupy in it, a concept component is screened per role,
// per side and per edge kind. Refused at VALIDATION, which is what makes it
// total — Parse, LoadRepoFile and config.Load all validate before anything
// compiles into a fact, so a rule refused here reaches no explainer.
//
// Four cases per role, and the expectation for the last three is derived from
// the schema's own Side and Vias rather than from a list written here:
//
//  1. no ownership declared — refused everywhere, because a verdict about the
//     edge has to state whether a member's methods are the member's.
//  2. ownership plus match globs — admitted, except where the role resolves the
//     SOURCE of an imports edge, which rides a dependency fact no predicate
//     selects.
//  3. ownership with no match globs — additionally refused where the role
//     resolves the TARGET of an imports edge, which names a path and reaches a
//     component only through the measured file grounding joins to match globs.
//  4. ownership, match globs and a name_pattern — refused in the same target
//     case, because grounding refuses a name-narrowed component outright.
func TestForms_EveryEdgeRoleScreensAConcept(t *testing.T) {
	covered := map[string]bool{}
	for _, form := range RuleForms {
		fixture, declared := formFixtures[form.Key]
		if !declared {
			t.Fatalf("rule form %q has no fixture — add one, so the edge-role screen is enumerated over every form the schema declares", form.Key)
		}
		if len(fixture.roles) == 0 || fixture.roles[0] != form.Key {
			t.Fatalf("fixture for %q must list its own form key first, got %v", form.Key, fixture.roles)
		}
		for _, role := range fixture.roles {
			covered[role] = true
			edge := walksEdges(t, form.Key, role)
			t.Run(form.Key+"/"+role, func(t *testing.T) {
				build := func(concept ConstraintComponent) *Declaration {
					rule := fixture.build(func(r string) string {
						if r == role {
							return predicateComponent
						}
						return pathComponent
					})
					components := enumerationComponents()
					components[0] = concept
					return &Declaration{Components: components, Rules: []ConstraintRule{rule}}
				}
				unstated := build(concept(""))
				switch problems := refusals(unstated.Problems()); {
				case edge && len(problems) != 1:
					t.Fatalf("problems = %v, want exactly one refusal: a concept in the %s role of a %s rule resolves against a measured edge and nothing says what it owns", unstated.Problems(), role, form.Key)
				case !edge && len(problems) != 0:
					t.Fatalf("problems = %v, want none: the %s form reads a member's own props, which is what a predicate selects", problems, form.Key)
				case edge:
					if !strings.Contains(problems[0], "nothing declares what it owns") {
						t.Errorf("the refusal must name the missing declaration, got %q", problems[0])
					}
					for _, want := range []string{predicateComponent, "(r)", "the " + role + " role", membershipFormKeys()} {
						if !strings.Contains(problems[0], want) {
							t.Errorf("refusal %q must name %q — the component, the rule, the role and what to use instead", problems[0], want)
						}
					}
				}
				if err := unstated.Validate(); edge != (err != nil) {
					t.Fatalf("Validate() error = %v, want error: %v", err, edge)
				}
				if !edge {
					return
				}

				side := roleSide(t, form.Key, role, fixture)
				imports := false
				for _, via := range RuleVias(fixture.build(func(string) string { return pathComponent })) {
					if PathTargetVia(via) {
						imports = true
					}
				}
				owned := concept(OwnsMethods)
				owned.Match = []string{"app/**"}
				assertRefused(t, build(owned), imports && side == SideSource,
					"a concept owning its methods is admitted unless the role sources an imports edge")

				noMatch := concept(OwnsMethods)
				assertRefused(t, build(noMatch), imports,
					"without match globs a concept can neither source an imports edge nor be grounded onto by one")

				narrowed := concept(OwnsMethods)
				narrowed.Match = []string{"app/**"}
				narrowed.NamePattern = "Jobs::Card"
				assertRefused(t, build(narrowed), imports,
					"grounding refuses a name-narrowed component, so an imports target reaches it through nothing")
			})
		}
	}
	for _, counterpart := range CounterpartRoles {
		if !covered[counterpart.Key] {
			t.Errorf("counterpart role %q is exercised by no fixture — every role a rule fills with a component must be enumerated", counterpart.Key)
		}
	}
	for key := range formFixtures {
		if !covered[key] {
			t.Errorf("fixture %q names no rule form — a stale fixture proves nothing about the schema", key)
		}
	}
}

func concept(owns string) ConstraintComponent {
	return ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "ViewComponent::Base"}, Owns: owns}
}

func assertRefused(t *testing.T, d *Declaration, want bool, why string) {
	t.Helper()
	got := refusals(d.Problems())
	if want && len(got) == 0 {
		t.Errorf("problems = %v, want a refusal: %s", d.Problems(), why)
	}
	if !want && len(got) != 0 {
		t.Errorf("problems = %v, want none: %s", got, why)
	}
}

// roleSide reads the end of the edge one role resolves against off the schema,
// never off a list written here.
func roleSide(t *testing.T, formKey, role string, fixture formFixture) string {
	t.Helper()
	r := fixture.build(func(string) string { return pathComponent })
	for _, form := range RuleForms {
		if form.Key == role && role == formKey {
			return form.Side(r)
		}
	}
	for _, counterpart := range CounterpartRoles {
		if counterpart.Key == role {
			return counterpart.Side(r)
		}
	}
	t.Fatalf("role %q of form %s is in neither schema table", role, formKey)
	return ""
}

// A component carrying a where predicate AND a match scope is still a predicate
// component: the predicate is what makes its membership unable to source an
// edge, and a path scope beside it narrows that membership rather than widening
// its reach.
func TestForms_APathScopeDoesNotExemptAPredicate(t *testing.T) {
	d := &Declaration{
		Components: []ConstraintComponent{
			{Name: predicateComponent, Match: []string{"app/components/**"}, Where: map[string]any{"superclass": "ViewComponent::Base"}},
			{Name: pathComponent, Match: []string{"app/models/**"}},
		},
		Rules: []ConstraintRule{base(ConstraintRule{Forbid: predicateComponent, To: pathComponent, Via: "imports"})},
	}
	if got := refusals(d.Problems()); len(got) != 1 {
		t.Fatalf("problems = %v, want the refusal", d.Problems())
	}
}

// The reserved kind key compiles to no property test, so a where carrying only
// it is not a predicate — and the screen must read the compiled predicate for
// the same reason the evaluator does, or the two disagree about what a
// component is.
func TestForms_AKindOnlyWhereIsNotAPredicate(t *testing.T) {
	d := &Declaration{
		Components: []ConstraintComponent{
			{Name: pathComponent, Match: []string{"app/models/**"}, Where: map[string]any{"kind": "storage"}},
			{Name: secondStep, Match: []string{"app/adapters/**"}},
		},
		Rules: []ConstraintRule{base(ConstraintRule{Forbid: pathComponent, To: secondStep, Via: "imports"})},
	}
	if got := refusals(d.Problems()); len(got) != 0 {
		t.Fatalf("problems = %v, want no refusal: a kind narrowing selects by fact kind, not by a measured property", got)
	}
}

// A recipe binds its roles at instantiation, so the screen has to fire on the
// EXPANDED declaration — where the bound selector is a concrete component —
// rather than on the recipe, which names roles and knows no selector at all.
func TestForms_ARecipeBindingCannotSmuggleAPredicateIntoAnEdgeRole(t *testing.T) {
	recipes := []Recipe{{
		Name:  "event-driven",
		Path:  "enola/recipes/event-driven.yaml",
		Roles: []RecipeRole{{Name: "events"}, {Name: "bus"}},
		Rules: []ConstraintRule{base(ConstraintRule{ID: "bus-owns-events", Protect: "events", Owners: []string{"bus"}, Via: "calls"})},
	}}
	files := []ConstraintsFile{{
		Path: "enola/constraints/orders.yaml",
		UseRecipe: []RecipeInstantiation{{
			Recipe: "event-driven",
			As:     "orders",
			Bind: map[string]RecipeBinding{
				"events": {Match: []string{"app/events/**"}},
				"bus":    {Where: map[string]any{"superclass": "EventBus"}},
			},
		}},
	}}
	merged, problems := ApplyRecipes(nil, files, recipes)
	if len(problems) != 0 {
		t.Fatalf("expansion problems = %v, want none: the instantiation is well formed", problems)
	}
	got := refusals(merged.Problems())
	if len(got) != 1 || !strings.Contains(got[0], "orders/bus") {
		t.Fatalf("problems = %v, want the refusal naming the expanded component orders/bus", merged.Problems())
	}
}

func refusals(problems []string) []string {
	var out []string
	for _, p := range problems {
		if strings.Contains(p, "is selected by a where predicate and") {
			out = append(out, p)
		}
	}
	return out
}

// The refusal's premise is that a predicate names class facts while the call
// graph connects methods. A component declaring kind: symbol selects the
// Owner#method facts themselves, so its members ARE the edge carriers and that
// mismatch cannot arise — the require_edge subject is exempt, and nothing else
// is, because that is the one role whose mechanics the claim was checked
// against.
func TestForms_ASymbolGranularPredicateMaySitInTheRequireEdgeSubject(t *testing.T) {
	symbolPredicate := ConstraintComponent{Name: predicateComponent, Kind: "symbol",
		Where: map[string]any{"symbol_kind": "getter"}}
	path := ConstraintComponent{Name: pathComponent, Match: []string{"app/utils/**"}}

	allowed := &Declaration{
		Components: []ConstraintComponent{symbolPredicate, path},
		Rules: []ConstraintRule{base(ConstraintRule{RequireEdge: predicateComponent, To: pathComponent,
			Via: "calls", Direction: "outbound"})},
	}
	if got := refusals(allowed.Problems()); len(got) != 0 {
		t.Fatalf("problems = %v, want none: a symbol-granular component's members carry their own edges", got)
	}
	if err := allowed.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want a clean declaration", err)
	}

	// The counterpart role resolves the component against the FAR end of a
	// measured edge, which is a path rather than a fact name whatever the
	// component's granularity. It stays refused.
	counterpart := &Declaration{
		Components: []ConstraintComponent{symbolPredicate, path},
		Rules: []ConstraintRule{base(ConstraintRule{RequireEdge: pathComponent, To: predicateComponent,
			Via: "calls", Direction: "outbound"})},
	}
	if got := refusals(counterpart.Problems()); len(got) != 1 {
		t.Fatalf("problems = %v, want the refusal on the to role", counterpart.Problems())
	}

	// The forbid subject reads each member's own Relations exactly as the
	// require_edge one does, so the same argument exempts it and nothing more.
	forbidSubject := &Declaration{
		Components: []ConstraintComponent{symbolPredicate, path},
		Rules:      []ConstraintRule{base(ConstraintRule{Forbid: predicateComponent, ToName: []string{"*.trackedFunction"}, Via: "calls"})},
	}
	if got := refusals(forbidSubject.Problems()); len(got) != 0 {
		t.Fatalf("problems = %v, want none: a symbol-granular component's members carry their own edges", got)
	}
	if err := forbidSubject.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want a clean declaration", err)
	}

	// A class-granular predicate in the same subject role is the case the
	// refusal was written for and keeps failing.
	classGranular := &Declaration{
		Components: []ConstraintComponent{
			{Name: predicateComponent, Where: map[string]any{"superclass": "Component"}}, path},
		Rules: []ConstraintRule{base(ConstraintRule{RequireEdge: predicateComponent, To: pathComponent,
			Via: "calls", Direction: "outbound"})},
	}
	if got := refusals(classGranular.Problems()); len(got) != 1 {
		t.Fatalf("problems = %v, want the refusal: a class predicate names facts the call graph does not connect", classGranular.Problems())
	}
}
