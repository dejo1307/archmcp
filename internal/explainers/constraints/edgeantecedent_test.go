package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The edge antecedent verdicts the implication a convention states as one:
// "a getter that works with promises carries the caching decorator". The
// antecedent is the call itself — the criterion, not a proxy for it — and the
// far end is a literal in the declaration, so nothing here resolves a second
// component against a measured edge.

func getterFact(name, file string, props map[string]any, calls ...string) facts.Fact {
	f := facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Props: props}
	for _, target := range calls {
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelCalls, Target: target})
	}
	return f
}

func promiseGetterStore() *facts.Store {
	store := facts.NewStore()
	store.Add(
		symbolComponentIntent("component-getters", "app/components/**"),
		ruleIntentProps("promise-getters-are-cached", map[string]any{
			"require":   "component-getters",
			"when_prop": "symbol_kind", "when_value": "getter",
			"when_edge_to": "*.getPromiseState *.reactiveUnwrap", "via": "calls",
			"must_prop": "decorators", "must_value": "cached",
			"because": "a getter that unwraps a promise recomputes on every read unless it memoizes"}),
		getterFact("app/components.BookCard.summary", "app/components/book-card.gts",
			map[string]any{"symbol_kind": "getter", "language": "typescript"},
			"app/components.BookCard.args", "app/utils.reactiveUnwrap"),
		getterFact("app/components.BookCard.state", "app/components/book-card.gts",
			map[string]any{"symbol_kind": "getter", "language": "typescript"},
			"app/components/book.getPromiseState"),
		getterFact("app/components.Badge.tone", "app/components/badge.gts",
			map[string]any{"symbol_kind": "getter", "decorators": "cached tracked", "language": "typescript"},
			"app/utils.reactiveUnwrap"),
		getterFact("app/components.Badge.label", "app/components/badge.gts",
			map[string]any{"symbol_kind": "getter", "language": "typescript"},
			"app/utils.titleize"),
		getterFact("app/components.Badge.submit", "app/components/badge.gts",
			map[string]any{"symbol_kind": "method", "language": "typescript"},
			"app/utils.reactiveUnwrap"),
	)
	return store
}

func TestExplain_RequireEdgeAntecedentSelectsByTheCallItself(t *testing.T) {
	insights, err := New().Explain(context.Background(), promiseGetterStore())
	if err != nil {
		t.Fatal(err)
	}
	violations := withTitlePrefix(insights, "Constraint promise-getters-are-cached violated")
	if len(violations) != 2 {
		t.Fatalf("violations = %+v, want the two undecorated promise getters", violations)
	}
	if violations[0].Title != "Constraint promise-getters-are-cached violated: app/components.BookCard.state must have decorators containing cached" {
		t.Errorf("title = %q", violations[0].Title)
	}
	if violations[1].Title != "Constraint promise-getters-are-cached violated: app/components.BookCard.summary must have decorators containing cached" {
		t.Errorf("title = %q", violations[1].Title)
	}
	if !strings.Contains(violations[1].Description,
		"whose symbol_kind contains getter and that makes a calls edge to *.getPromiseState or *.reactiveUnwrap") {
		t.Errorf("the verdict must state both antecedents it was selected by: %q", violations[1].Description)
	}
	// The edge that selected the member rides its evidence: a reader opens the
	// finding and sees which call put the getter in scope, not only what it lacks.
	if got := violations[1].Evidence[0].Detail; got != "calls edge to app/utils.reactiveUnwrap, missing decorators cached" {
		t.Errorf("evidence detail = %q, want the witnessing edge", got)
	}
	for _, in := range insights {
		switch {
		case strings.Contains(in.Title, "Badge.tone"):
			t.Errorf("a decorated promise getter is satisfied, not reported: %q", in.Title)
		case strings.Contains(in.Title, "Badge.label"):
			t.Errorf("a getter that calls neither helper is out of scope, not in breach: %q", in.Title)
		case strings.Contains(in.Title, "Badge.submit"):
			t.Errorf("the prop antecedent still narrows: a method is out of scope: %q", in.Title)
		}
	}
}

// Two antecedents narrow together. A member that satisfies one and not the
// other is out of the rule's scope — the same reading every other narrowing in
// this vocabulary has, where each field ANDs and none widens.
func TestExplain_RequireAntecedentsNarrowTogether(t *testing.T) {
	insights, err := New().Explain(context.Background(), promiseGetterStore())
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for _, in := range withTitlePrefix(insights, "Constraint promise-getters-are-cached violated") {
		for _, ev := range in.Evidence {
			selected[ev.Symbol] = true
		}
	}
	if selected["app/components.Badge.submit"] {
		t.Error("Badge.submit makes the edge but fails the prop clause — the edge clause must not widen the scope")
	}
	if selected["app/components.Badge.label"] {
		t.Error("Badge.label passes the prop clause but makes no such edge — the prop clause must not widen the scope")
	}
	if !selected["app/components.BookCard.summary"] || !selected["app/components.BookCard.state"] {
		t.Errorf("both clauses hold for the promise getters, so both are in scope: %v", selected)
	}
}

// The honest-failure case. This form only works where the selector and the
// edges live on the same fact: a Ruby class's calls ride its Owner#method
// facts, so a component of classes answers nothing the antecedent asks. The
// antecedent then selects nobody and every member passes unasked — which is
// exactly the vacuous pass this explainer exists to prevent, so the rule says
// so instead of reporting nothing.
func TestExplain_RequireEdgeAntecedentSaysSoWhenNoMemberCarriesTheEdge(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("service-classes", map[string]string{"symbol_kind": "class"},
			map[string]any{"match": "app/services/**", "kind": "symbol"}),
		ruleIntentProps("promise-services-are-cached", map[string]any{
			"require":      "service-classes",
			"when_edge_to": "*.reactiveUnwrap", "via": "calls",
			"must_prop": "decorators", "must_value": "cached",
			"because": "a service that unwraps a promise recomputes on every read unless it memoizes"}),
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::Invoice", File: "app/services/billing/invoice.rb",
			Props: map[string]any{"symbol_kind": "class", "language": "ruby"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::Refund", File: "app/services/billing/refund.rb",
			Props: map[string]any{"symbol_kind": "class", "language": "ruby"}},
		// The calls are measured — on the methods, which the class component
		// does not select. The rule cannot see them, and must not pretend to.
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::Invoice#total", File: "app/services/billing/invoice.rb",
			Props:     map[string]any{"symbol_kind": "method", "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Promise.reactiveUnwrap"}}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if got := withTitlePrefix(insights, "Constraint promise-services-are-cached violated"); len(got) != 0 {
		t.Fatalf("no member was asked anything, so none may be reported in breach: %+v", got)
	}
	skips := withTitlePrefix(insights, "require rule promise-services-are-cached skipped:")
	if len(skips) != 1 {
		t.Fatalf("want exactly one skip advisory, got %+v", insights)
	}
	skip := skips[0]
	if skip.Title != "require rule promise-services-are-cached skipped: no member of service-classes makes a calls edge the antecedent selects" {
		t.Errorf("title = %q", skip.Title)
	}
	if skip.Confidence != requireSkipConfidence {
		t.Errorf("confidence = %v, want the skip advisory's %v", skip.Confidence, requireSkipConfidence)
	}
	if !strings.Contains(skip.Description, "not one of the 2 members") {
		t.Errorf("the advisory must count what went unasked: %q", skip.Description)
	}
	if !strings.Contains(skip.Description, "Because: a service that unwraps a promise") {
		t.Errorf("the advisory carries the rule's rationale: %q", skip.Description)
	}
}

// The blindness the advisory exists for does not announce itself as an absence
// of edges. A Ruby class fact carries the calls its class BODY makes — the
// include, the attr_reader, the validates — while the calls the rule asks
// about ride the methods, which this component does not select. Counting
// relations of the read kind would find those incidental macros and certify
// the component as answerable; the antecedent, which is what actually decides
// membership, still selects nobody. The advisory is read off the antecedent's
// own answers, so one macro call cannot buy silence for the whole component.
func TestExplain_RequireEdgeAdvisoryIsNotBoughtOffByAnIncidentalEdge(t *testing.T) {
	rubyClass := func(name, file string, calls ...string) facts.Fact {
		f := facts.Fact{Kind: facts.KindSymbol, Name: name, File: file,
			Props: map[string]any{"symbol_kind": "class", "language": "ruby"}}
		for _, target := range calls {
			f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelCalls, Target: target})
		}
		return f
	}
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("service-classes", map[string]string{"symbol_kind": "class"},
			map[string]any{"match": "app/services/**", "kind": "symbol"}),
		ruleIntentProps("promise-services-are-cached", map[string]any{
			"require":      "service-classes",
			"when_edge_to": "*.reactive_unwrap", "via": "calls",
			"must_prop": "decorators", "must_value": "memoized",
			"because": "a service that unwraps a promise recomputes on every read unless it memoizes"}),
		rubyClass("Billing::Invoice", "app/services/billing/invoice.rb", "ActiveSupport::Concern.include", "Billing::Invoice.attr_reader"),
		rubyClass("Billing::Refund", "app/services/billing/refund.rb"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::Invoice#total", File: "app/services/billing/invoice.rb",
			Props:     map[string]any{"symbol_kind": "method", "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Promise.reactive_unwrap"}}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if got := withTitlePrefix(insights, "Constraint promise-services-are-cached violated"); len(got) != 0 {
		t.Fatalf("no member was asked anything, so none may be reported in breach: %+v", got)
	}
	skips := withTitlePrefix(insights, "require rule promise-services-are-cached skipped:")
	if len(skips) != 1 {
		t.Fatalf("a class-body macro is not an answer to the antecedent — want the advisory, got %+v", insights)
	}
	if !strings.Contains(skips[0].Description, "not one of the 2 members") {
		t.Errorf("the advisory must count what went unasked: %q", skips[0].Description)
	}
}

// One reader, one question. The antecedent is evaluated on the name-keyed
// representative of each member — the fact the verdict would evidence — so the
// advisory must be answered on those same facts. A second fact under the same
// name, which nothing in this form ever reads, cannot certify the component as
// answerable on behalf of a representative that carries no such edge.
func TestExplain_RequireEdgeAdvisoryReadsTheFactsTheAntecedentReads(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		symbolComponentIntent("component-getters", "app/components/**"),
		ruleIntentProps("promise-getters-are-cached", map[string]any{
			"require":      "component-getters",
			"when_edge_to": "*.reactiveUnwrap", "via": "calls",
			"must_prop": "decorators", "must_value": "cached",
			"because": "a getter that unwraps a promise recomputes on every read unless it memoizes"}),
		getterFact("app/components.BookCard.summary", "app/components/book-card-a.gts",
			map[string]any{"symbol_kind": "getter"}, "app/utils.titleize"),
		// Same member name, a later file: the class is reopened, and only this
		// fact carries the call. firstFactByName never hands it to the
		// antecedent, so it may not answer for the member either.
		getterFact("app/components.BookCard.summary", "app/components/book-card-b.gts",
			map[string]any{"symbol_kind": "getter"}, "app/utils.reactiveUnwrap"),
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if got := withTitlePrefix(insights, "Constraint promise-getters-are-cached violated"); len(got) != 0 {
		t.Fatalf("the fact the antecedent reads makes no such edge, so nobody was asked: %+v", got)
	}
	if got := withTitlePrefix(insights, "require rule promise-getters-are-cached skipped:"); len(got) != 1 {
		t.Fatalf("a fact the antecedent never reads cannot answer for the component — want the advisory, got %+v", insights)
	}
}

// A component one of whose members the antecedent selects has answered it, and
// a member of that component making no such edge is out of scope rather than
// skipped: the advisory is asked of the component once, never turned into a
// per-member excuse that would swallow real absences.
func TestExplain_RequireEdgeAntecedentAsksTheComponentOnce(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		symbolComponentIntent("component-getters", "app/components/**"),
		ruleIntentProps("promise-getters-are-cached", map[string]any{
			"require":      "component-getters",
			"when_edge_to": "*.reactiveUnwrap", "via": "calls",
			"must_prop": "decorators", "must_value": "cached",
			"because": "a getter that unwraps a promise recomputes on every read unless it memoizes"}),
		getterFact("app/components.BookCard.summary", "app/components/book-card.gts",
			map[string]any{"symbol_kind": "getter"}, "app/utils.reactiveUnwrap"),
		getterFact("app/components.Badge.label", "app/components/badge.gts",
			map[string]any{"symbol_kind": "getter"}),
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if got := withTitlePrefix(insights, "require rule promise-getters-are-cached skipped:"); len(got) != 0 {
		t.Fatalf("one measured edge in the membership answers the antecedent — no skip: %+v", got)
	}
	violations := withTitlePrefix(insights, "Constraint promise-getters-are-cached violated")
	if len(violations) != 1 || !strings.Contains(violations[0].Title, "BookCard.summary") {
		t.Fatalf("violations = %+v, want the one member that makes the edge and lacks the decorator", violations)
	}
}

// The dialect is the one require_name speaks, and the suffix form is what
// makes it usable against a real graph: a call target arrives qualified with
// the module that defines it, and the declaration should not have to know
// where the helper lives.
func TestExplain_RequireEdgeAntecedentMatchesQualifiedTargetsBySuffix(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		symbolComponentIntent("component-getters", "app/components/**"),
		ruleIntentProps("promise-getters-are-cached", map[string]any{
			"require":      "component-getters",
			"when_edge_to": "*.reactiveUnwrap", "via": "calls",
			"must_prop": "decorators", "must_value": "cached",
			"because": "a getter that unwraps a promise recomputes on every read unless it memoizes"}),
		getterFact("app/components.BookCard.summary", "app/components/book-card.gts",
			map[string]any{"symbol_kind": "getter"}, "ember_app/app/utils.reactiveUnwrap"),
		// Whole-member matching, never substring: the suffix is anchored, so a
		// helper whose name merely ends with the same letters is not the helper.
		getterFact("app/components.Badge.tone", "app/components/badge.gts",
			map[string]any{"symbol_kind": "getter"}, "app/utils.deepReactiveUnwrapAll"),
		// The edge kind is the declared one and no other: an imports edge to the
		// same name selects nothing.
		getterFact("app/components.Badge.label", "app/components/badge.gts",
			map[string]any{"symbol_kind": "getter"},
		),
		facts.Fact{Kind: facts.KindSymbol, Name: "app/components.Badge.title", File: "app/components/badge.gts",
			Props:     map[string]any{"symbol_kind": "getter"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "app/utils.reactiveUnwrap"}}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	violations := withTitlePrefix(insights, "Constraint promise-getters-are-cached violated")
	if len(violations) != 1 || !strings.Contains(violations[0].Title, "BookCard.summary") {
		t.Fatalf("violations = %+v, want only the getter whose calls edge names the helper", violations)
	}
}

// The pre-edit contract states the rule in the same words the verdict does, so
// an agent about to add a getter reads the whole antecedent rather than half.
func TestContractFor_StatesTheEdgeAntecedent(t *testing.T) {
	bindings, asked := ContractFor(promiseGetterStore(), "app/components/book-card.gts")
	if !asked || len(bindings) != 1 || len(bindings[0].Rules) != 1 {
		t.Fatalf("bindings = %+v", bindings)
	}
	want := "members of component-getters whose symbol_kind contains getter and that makes a calls edge to *.getPromiseState or *.reactiveUnwrap must have decorators containing cached"
	if got := bindings[0].Rules[0].Statement; got != want {
		t.Errorf("statement = %q, want %q", got, want)
	}
}

// The same bounded dialect at its third declaration site: a component's
// name_pattern. A name narrowing is not a where: predicate — it says nothing
// about what a fact carries — so a component selected this way is party to the
// edge forms, which is what makes "no constructor calls a fetcher" declarable
// at all.

func namedSymbol(name, file string, calls ...string) facts.Fact {
	f := facts.Fact{Kind: facts.KindSymbol, Name: name, File: file}
	for _, target := range calls {
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelCalls, Target: target})
	}
	return f
}

func namePatternComponentIntent(name, match, pattern string) facts.Fact {
	f := symbolComponentIntent(name, match)
	f.Props["name_pattern"] = pattern
	return f
}

func constructorFetchStore(subject, counterpart facts.Fact) *facts.Store {
	store := facts.NewStore()
	store.Add(
		subject,
		counterpart,
		ruleIntent("constructors-do-not-fetch", "constructors", "fetchers", "calls",
			"a constructor that fetches makes the first render wait on the network"),
		namedSymbol("app/components.BookCard.constructor", "app/components/book-card.gts", "app/services.fetchBooks"),
		namedSymbol("app/components.Badge.constructor", "app/components/badge.gts", "app/utils.titleize"),
		namedSymbol("app/components.BookCard.load", "app/components/book-card.gts", "app/services.fetchBooks"),
		namedSymbol("app/services.fetchBooks", "app/services/books.ts"),
		namedSymbol("app/utils.titleize", "app/utils/text.ts"),
	)
	return store
}

func fetchViolationWitnesses(t *testing.T, store *facts.Store) []string {
	t.Helper()
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var witnesses []string
	for _, insight := range withTitlePrefix(insights, "Constraint constructors-do-not-fetch violated") {
		for _, e := range insight.Evidence {
			witnesses = append(witnesses, e.Symbol+" -> "+e.Fact)
		}
	}
	return witnesses
}

// The rule the dialect exists for. `*.constructor` names a family no exact
// name could, and the members it selects are judged on the edges they make —
// so the constructor that fetches is a violation and the constructor that does
// not, and the ordinary method that does, are both left alone.
func TestExplain_NamePatternSuffixFamilyIsTheSubjectOfAnEdgeRule(t *testing.T) {
	witnesses := fetchViolationWitnesses(t, constructorFetchStore(
		namePatternComponentIntent("constructors", "app/**", "*.constructor"),
		symbolComponentIntent("fetchers", "app/services/**"),
	))
	if len(witnesses) != 1 || witnesses[0] != "app/components.BookCard.constructor -> app/services.fetchBooks" {
		t.Fatalf("witnesses = %v, want the one constructor that calls a fetcher", witnesses)
	}
}

// A prefix family reads the same way in the counterpart role, where a
// component's names are what an edge TARGET is matched against.
func TestExplain_NamePatternPrefixFamilyIsTheCounterpartOfAnEdgeRule(t *testing.T) {
	witnesses := fetchViolationWitnesses(t, constructorFetchStore(
		namePatternComponentIntent("constructors", "app/**", "*.constructor"),
		namePatternComponentIntent("fetchers", "app/**", "app/services.fetch*"),
	))
	if len(witnesses) != 1 || witnesses[0] != "app/components.BookCard.constructor -> app/services.fetchBooks" {
		t.Fatalf("witnesses = %v, want the constructor whose call lands in the prefix family", witnesses)
	}
}

// A starless pattern still selects one fact and only that one, in the same
// rule and over the same store — so the family forms are an addition to what
// name_pattern did, never a reinterpretation of it.
func TestExplain_StarlessNamePatternStillSelectsExactlyOneFact(t *testing.T) {
	fetching := fetchViolationWitnesses(t, constructorFetchStore(
		namePatternComponentIntent("constructors", "app/**", "app/components.BookCard.constructor"),
		symbolComponentIntent("fetchers", "app/services/**"),
	))
	if len(fetching) != 1 || fetching[0] != "app/components.BookCard.constructor -> app/services.fetchBooks" {
		t.Fatalf("witnesses = %v, want the exactly-named constructor", fetching)
	}
	quiet := fetchViolationWitnesses(t, constructorFetchStore(
		namePatternComponentIntent("constructors", "app/**", "app/components.Badge.constructor"),
		symbolComponentIntent("fetchers", "app/services/**"),
	))
	if len(quiet) != 0 {
		t.Fatalf("witnesses = %v, want none — the other constructor calls no fetcher, and an exact pattern reaches nothing else", quiet)
	}
}

// The regression, over the code path rather than over the matcher: every
// name_pattern this repository declares today is starless, and for a starless
// pattern the membership walk must select the same facts the equality test it
// replaced would have selected. Re-derived here for every declared pattern and
// for every fact name in the store used as one.
func TestResolveMembership_StarlessNamePatternClassifiesExactlyAsEqualityDid(t *testing.T) {
	names := []string{
		"app/domain/billing",
		"app/domain/billing_report",
		"billing",
		"Billing",
		"BillingSerializer",
		"runtime-route: GET /legacy/export",
		"runtime-route: GET /legacy/exports",
		"rbs-signature: Legacy::Export#run",
	}
	store := facts.NewStore()
	for _, name := range names {
		store.Add(facts.Fact{Kind: facts.KindModule, Name: name, File: "app/domain/" + name})
	}
	patterns := append([]string{"constructor", "app/domain/billin"}, names...)
	for _, pattern := range patterns {
		if strings.Contains(pattern, "*") {
			t.Fatalf("%q carries a star — this case is the starless regression, and a family pattern does not belong in it", pattern)
		}
		got, _ := resolveMembership(store, component{name: "c", match: []string{"app/domain/**"}, namePattern: pattern})
		want := map[string]bool{}
		for _, name := range names {
			if name == pattern {
				want[name] = true
			}
		}
		if len(got) != len(want) {
			t.Fatalf("pattern %q selected %v, want %v — equality is what a starless pattern always meant", pattern, got, want)
		}
		for name := range want {
			if !got[name] {
				t.Errorf("pattern %q did not select %q, which equals it", pattern, name)
			}
		}
		for name := range got {
			if !want[name] {
				t.Errorf("pattern %q selected %q, which does not equal it", pattern, name)
			}
		}
	}
}

// The dialect does not move the grounding guard. A path-granular edge target
// resolves to a FILE, and a file cannot show which of the facts measured in it
// the edge landed on — so a name-narrowed component grounds no path target,
// exactly as it did when the narrowing could only be one name. The same store
// with the narrowing removed does ground, which is what makes the silence
// above a decision rather than a store that proves nothing.
func TestExplain_NamePatternedComponentGroundsNoPathTarget(t *testing.T) {
	build := func(serializers facts.Fact) *facts.Store {
		store := facts.NewStore()
		store.Add(
			componentIntent("domain", "app/domain/**"),
			serializers,
			ruleIntent("domain-imports-no-serializers", "domain", "serializers", "imports",
				"the domain owns no wire format"),
			facts.Fact{Kind: facts.KindModule, Name: "app/domain/billing", File: "app/domain/billing",
				Relations: []facts.Relation{{Kind: facts.RelImports, Target: "app/serializers/book"}}},
			facts.Fact{Kind: facts.KindSymbol, Name: "BookSerializer", File: "app/serializers/book.ts"},
		)
		return store
	}
	witnesses := func(store *facts.Store) []facts.Insight {
		insights, err := New().Explain(context.Background(), store)
		if err != nil {
			t.Fatal(err)
		}
		return withTitlePrefix(insights, "Constraint domain-imports-no-serializers violated")
	}
	narrowed := witnesses(build(namePatternComponentIntent("serializers", "app/serializers/**", "*Serializer")))
	if len(narrowed) != 0 {
		t.Fatalf("violations = %+v, want none — the resolved file hosts a member, which is not evidence the import landed on one", narrowed)
	}
	wide := witnesses(build(symbolComponentIntent("serializers", "app/serializers/**")))
	if len(wide) != 1 {
		t.Fatalf("violations = %+v, want exactly one — without the name narrowing this import does ground, so the case above withholds a verdict it could have reached", wide)
	}
}
