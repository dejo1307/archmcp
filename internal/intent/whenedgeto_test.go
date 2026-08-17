package intent

import (
	"strings"
	"testing"
)

// The edge antecedent's declaration surface. What a rule may say about the far
// end of a member's own edge is bounded at parse time — one dialect, one
// matcher — so the cases below pin both halves: which declarations are
// admitted, and that the evaluator's matcher is defined over exactly those.

func requireWithEdge(body string) string {
	return "components:\n  - name: getters\n    match: [app/components/**]\nrules:\n  - id: r\n    require: getters\n" +
		body + "    must_prop_contain: {prop: decorators, value: cached}\n    because: promise getters memoize\n"
}

func TestParse_WhenEdgeToIsALiteralInTheBoundedDialect(t *testing.T) {
	cases := map[string]struct {
		input   string
		wantErr string
	}{
		"exact target with a via parses": {
			input: requireWithEdge("    when_edge_to: [reactiveUnwrap]\n    via: calls\n"),
		},
		"suffix target with a via parses": {
			input: requireWithEdge("    when_edge_to: [\"*.reactiveUnwrap\", \"*.getPromiseState\"]\n    via: calls\n"),
		},
		"prefix target with a via parses": {
			input: requireWithEdge("    when_edge_to: [\"promise*\"]\n    via: implements\n"),
		},
		"both antecedents together parse — they narrow, never widen": {
			input: requireWithEdge("    when_prop_contains: {prop: symbol_kind, value: getter}\n    when_edge_to: [\"*.reactiveUnwrap\"]\n    via: calls\n"),
		},
		"no via names no edge kind": {
			input:   requireWithEdge("    when_edge_to: [reactiveUnwrap]\n"),
			wantErr: "when_edge_to needs a via",
		},
		"a via outside the edge vocabulary": {
			input:   requireWithEdge("    when_edge_to: [reactiveUnwrap]\n    via: reads\n"),
			wantErr: "when_edge_to needs a via",
		},
		"a via with no edge antecedent still belongs to the edge forms": {
			input:   requireWithEdge("    when_prop_contains: {prop: symbol_kind, value: getter}\n    via: calls\n"),
			wantErr: "via belongs to the edge forms",
		},
		"a general glob is not the dialect": {
			input:   requireWithEdge("    when_edge_to: [\"*react*Unwrap\"]\n    via: calls\n"),
			wantErr: "must be a literal edge target",
		},
		"a bare star names everything and is refused": {
			input:   requireWithEdge("    when_edge_to: [\"*\"]\n    via: calls\n"),
			wantErr: "must be a literal edge target",
		},
		"a character class is not the dialect": {
			input:   requireWithEdge("    when_edge_to: [\"get[PS]romiseState\"]\n    via: calls\n"),
			wantErr: "must be a literal edge target",
		},
		"a space would not survive the compiled fact's set encoding": {
			input:   requireWithEdge("    when_edge_to: [\"two names\"]\n    via: calls\n"),
			wantErr: "must carry no whitespace",
		},
		"nor would a tab": {
			input:   requireWithEdge("    when_edge_to: [\"two\\tnames\"]\n    via: calls\n"),
			wantErr: "must carry no whitespace",
		},
		"nor a newline, which the set encoding splits on exactly like a space": {
			input:   requireWithEdge("    when_edge_to: [\"two\\nnames\"]\n    via: calls\n"),
			wantErr: "must carry no whitespace",
		},
		"nor a no-break space, which reads as one name and splits as two": {
			input:   requireWithEdge("    when_edge_to: [\"two\\u00a0names\"]\n    via: calls\n"),
			wantErr: "must carry no whitespace",
		},
		"the antecedent belongs to the two require forms and nothing else": {
			input: "components:\n  - name: getters\n    match: [app/components/**]\nrules:\n  - id: r\n    require_name: getters\n" +
				"    pattern: \"*Getter\"\n    when_edge_to: [reactiveUnwrap]\n    via: calls\n    because: x\n",
			wantErr: "when_edge_to belongs to the require and require_edge forms",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := Parse([]byte(tc.input))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want a clean parse, got: %v", err)
				}
				if d == nil {
					t.Fatal("a clean parse must return a declaration")
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error naming %q, got a declaration: %+v", tc.wantErr, d)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error must name the problem %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// The far end is a literal, and a component name in that slot is a literal
// too: it matches the edge target that happens to spell it, and nothing
// resolves it against the declared component. This is the whole reason the
// edge antecedent stays out of the edge-walking screen — there is no second
// component to own, and no source basis to state for one.
func TestParse_WhenEdgeToNamingAComponentIsStillALiteral(t *testing.T) {
	d, err := Parse([]byte("components:\n  - name: getters\n    match: [app/components/**]\n  - name: helpers\n    match: [app/utils/**]\n" +
		"rules:\n  - id: r\n    require: getters\n    when_edge_to: [helpers]\n    via: calls\n" +
		"    must_prop_contain: {prop: decorators, value: cached}\n    because: promise getters memoize\n"))
	if err != nil {
		t.Fatalf("a literal that spells a component name is admitted like any other: %v", err)
	}
	rule := d.Rules[0]
	if len(rule.WhenEdgeTo) != 1 || rule.WhenEdgeTo[0] != "helpers" {
		t.Fatalf("when_edge_to = %v, want the literal as written", rule.WhenEdgeTo)
	}
	for _, role := range CounterpartRoles {
		for _, named := range role.Names(rule) {
			if named == "helpers" {
				t.Fatalf("%s must not hold the edge antecedent's literal — a counterpart role is resolved against a component", role.Key)
			}
		}
	}
}

// The compiled fact is a function of the declared SET, like every other
// multi-valued role: the targets are sorted into one space-separated prop, so
// two declarations that differ only in the order they list the helpers compile
// to the same fact and leave the snapshot digest where it was.
func TestCompileFacts_WhenEdgeToIsASortedSet(t *testing.T) {
	compile := func(targets []string) map[string]any {
		decl := constraintDecl(
			[]ConstraintComponent{{Name: "getters", Match: []string{"app/components/**"}, Kind: "symbol"}},
			[]ConstraintRule{{
				ID: "promise-getters-are-cached", Require: "getters",
				WhenEdgeTo:      targets,
				Via:             "calls",
				MustPropContain: &PropMatch{Prop: "decorators", Value: "cached"},
				Because:         "a getter that unwraps a promise recomputes on every read unless it memoizes",
			}},
		)
		if err := decl.Validate(); err != nil {
			t.Fatalf("a well-formed edge antecedent must validate, got: %v", err)
		}
		ff := CompileFacts(decl)
		return ff[len(ff)-1].Props
	}
	props := compile([]string{"*.reactiveUnwrap", "*.getPromiseState"})
	if props["when_edge_to"] != "*.getPromiseState *.reactiveUnwrap" || props["via"] != "calls" {
		t.Errorf("require fact = %+v", props)
	}
	if reordered := compile([]string{"*.getPromiseState", "*.reactiveUnwrap"}); reordered["when_edge_to"] != props["when_edge_to"] {
		t.Errorf("declaration order must not reach the fact: %v vs %v", reordered["when_edge_to"], props["when_edge_to"])
	}
	bare := constraintDecl(
		[]ConstraintComponent{{Name: "getters", Match: []string{"app/components/**"}}},
		[]ConstraintRule{{ID: "r", Require: "getters", MustPropContain: &PropMatch{Prop: "columns", Value: "id"}, Because: "x"}},
	)
	ff := CompileFacts(bare)
	if _, present := ff[len(ff)-1].Props["when_edge_to"]; present {
		t.Errorf("an absent edge antecedent must compile no prop: %+v", ff[len(ff)-1].Props)
	}
}

// The screen and the round trip are the same predicate, rune for rune. The
// compiled rule holds the targets as one whitespace-separated prop and the
// evaluator reads it back with strings.Fields, so any rune Fields splits on
// must be refused at the declaration: a target that validates as one name and
// evaluates as two is a rule that does not say what it compiled to. The
// screen is unicode.IsSpace, which is exactly what Fields splits on.
func TestValidate_WhenEdgeToRefusesEveryRuneTheSetSplitWouldEat(t *testing.T) {
	for _, r := range []rune{' ', '\t', '\n', '\v', '\f', '\r', '\u0085', '\u00a0', '\u1680', '\u2028', '\u2029', '\u3000'} {
		target := "utils.reactive" + string(r) + "Unwrap"
		if len(strings.Fields(target)) == 1 {
			t.Fatalf("premise: strings.Fields must split %+q, or the screen is not the round trip's", target)
		}
		decl := constraintDecl(
			[]ConstraintComponent{{Name: "getters", Match: []string{"app/components/**"}, Kind: "symbol"}},
			[]ConstraintRule{{
				ID: "promise-getters-are-cached", Require: "getters",
				WhenEdgeTo:      []string{target},
				Via:             "calls",
				MustPropContain: &PropMatch{Prop: "decorators", Value: "cached"},
				Because:         "a getter that unwraps a promise recomputes on every read unless it memoizes",
			}},
		)
		err := decl.Validate()
		if err == nil {
			t.Errorf("%+q validated clean and would compile to %+q — the screen must be the round trip's", target, strings.Fields(target))
			continue
		}
		if !strings.Contains(err.Error(), "must carry no whitespace") {
			t.Errorf("%+q: error must name the whitespace, got: %v", target, err)
		}
	}
}

// One dialect, one matcher. ValidNamePattern says what a declaration may write
// and MatchBoundedName says what the evaluator does with it, so a pattern the
// validator admits must have exactly one documented reading and a pattern it
// refuses must never be reachable at all.
func TestNamePatternDialect_ValidatorAndMatcherReadOneGrammar(t *testing.T) {
	admitted := map[string]struct{ matches, misses []string }{
		"reactiveUnwrap": {
			matches: []string{"reactiveUnwrap"},
			misses:  []string{"app/utils.reactiveUnwrap", "reactiveUnwrapAll", "unwrap"},
		},
		"*.reactiveUnwrap": {
			matches: []string{"app/utils.reactiveUnwrap", "a.b.c.reactiveUnwrap"},
			misses:  []string{"reactiveUnwrap", "app/utils.reactiveUnwrapAll"},
		},
		"promise*": {
			matches: []string{"promiseState", "promise"},
			misses:  []string{"getPromiseState"},
		},
	}
	for pattern, want := range admitted {
		if !ValidNamePattern(pattern) {
			t.Fatalf("ValidNamePattern(%q) = false, want the dialect to admit it", pattern)
		}
		for _, name := range want.matches {
			if !MatchBoundedName(name, pattern) {
				t.Errorf("MatchBoundedName(%q, %q) = false, want true", name, pattern)
			}
		}
		for _, name := range want.misses {
			if MatchBoundedName(name, pattern) {
				t.Errorf("MatchBoundedName(%q, %q) = true, want false", name, pattern)
			}
		}
	}
	for _, refused := range []string{"", "*", "**", "a*b*", "get[PS]romiseState", "unwrap?", "{a,b}"} {
		if ValidNamePattern(refused) {
			t.Errorf("ValidNamePattern(%q) = true — the matcher has one reading of a pattern, and this is not in it", refused)
		}
	}
}

// The dialect's third declaration site is a component's name_pattern, and it
// is held to the same screen the other two are: what a component may name is
// exactly what MatchBoundedName has a reading for, so a family a declaration
// writes is a family the evaluator recognizes.

func componentNamePattern(pattern string) string {
	return "components:\n  - name: constructors\n    match: [app/**]\n    kind: symbol\n    name_pattern: " + pattern + "\n" +
		"rules:\n  - id: r\n    forbid_fact: constructors\n    because: a constructor fetches nothing\n"
}

func TestParse_ComponentNamePatternSpeaksTheBoundedDialect(t *testing.T) {
	cases := map[string]struct {
		pattern string
		wantErr string
	}{
		"an exact name parses, as it always did": {pattern: "constructor"},
		"a suffix family parses":                 {pattern: `"*.constructor"`},
		"a prefix family parses":                 {pattern: `"fetch*"`},
		"a general glob is not the dialect": {
			pattern: `"*fetch*"`,
			wantErr: `name_pattern "*fetch*" must be an exact name, a prefix*, or a *suffix`,
		},
		"a bare star names everything and is refused": {
			pattern: `"*"`,
			wantErr: `name_pattern "*" must be an exact name`,
		},
		"a second star would be backtracking the evaluator does not do": {
			pattern: `"a*b*"`,
			wantErr: `name_pattern "a*b*" must be an exact name`,
		},
		"a character class is not the dialect": {
			pattern: `"fetch[AB]"`,
			wantErr: `name_pattern "fetch[AB]" must be an exact name`,
		},
		"a brace set is not the dialect": {
			pattern: `"{a,b}"`,
			wantErr: `name_pattern "{a,b}" must be an exact name`,
		},
		"a single-character wildcard is not the dialect": {
			pattern: `"fetch?"`,
			wantErr: `name_pattern "fetch?" must be an exact name`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := Parse([]byte(componentNamePattern(tc.pattern)))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want a clean parse, got: %v", err)
				}
				if d == nil {
					t.Fatal("a clean parse must return a declaration")
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error naming %q, got a declaration: %+v", tc.wantErr, d)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error must name the problem %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// A recipe binding writes the same selector a component writes, so it meets
// the same screen — on the EXPANDED declaration, naming the instance and role
// that wrote the pattern rather than the generated component nobody typed.
func TestLoadRepoFile_BindingNamePatternIsScreenedAndCitesItsRole(t *testing.T) {
	bad := strings.Replace(ordersInstantiation,
		`events:   { match: ["app/events/orders/**"] }`,
		`events:   { match: ["app/events/orders/**"], name_pattern: "Order*Event*" }`, 1)
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": bad},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("an out-of-dialect binding name_pattern must be an error")
	}
	if !strings.Contains(err.Error(), "use_recipe orders-events (recipe event-driven) role events") {
		t.Fatalf("the error must cite the instance and role that declared the pattern, got: %v", err)
	}
	if !strings.Contains(err.Error(), `name_pattern "Order*Event*"`) {
		t.Fatalf("the error must quote the refused pattern, got: %v", err)
	}
}

// The screen is the matcher's, which means it is narrower than "any string":
// a fact name carrying a glob metacharacter — Ruby's Config#[] is the real
// one — is no longer declarable as an exact name_pattern. That is the price of
// refusing to admit a pattern the evaluator would silently misread, and it is
// paid as a named error at declaration time rather than as a wrong verdict.
func TestParse_ComponentNamePatternRefusesAnExactNameCarryingAMetacharacter(t *testing.T) {
	_, err := Parse([]byte(componentNamePattern(`"Config#[]"`)))
	if err == nil {
		t.Fatal("an exact name carrying a metacharacter must be refused, not silently reread")
	}
	if !strings.Contains(err.Error(), `name_pattern "Config#[]"`) {
		t.Fatalf("the error must quote the name it refused, got: %v", err)
	}
}

// Exact names keep matching exactly, and structurally rather than by luck: the
// dialect's starless case IS string equality, so a declaration written before
// the dialect reached components reads every name the way it always did. The
// patterns below are every name_pattern this repository's declarations,
// fixtures and documentation carry today.
func TestMatchBoundedName_StarlessPatternIsStringEquality(t *testing.T) {
	declared := []string{
		"app/domain/billing",
		"runtime-route: GET /legacy/export",
		"rbs-signature: Legacy::Export#run",
		"Billing",
	}
	neighbours := []string{
		"app/domain/billing_report",
		"app/domain/billing/report",
		"billing",
		"runtime-route: GET /legacy/exports",
		"runtime-route: GET /legacy/export ",
		"rbs-signature: Legacy::Export#runs",
		"BillingSerializer",
		"",
	}
	for _, pattern := range declared {
		if strings.Contains(pattern, "*") {
			t.Fatalf("%q carries a star — this case is the starless regression and a family pattern does not belong in it", pattern)
		}
		if !ValidNamePattern(pattern) {
			t.Fatalf("ValidNamePattern(%q) = false — a name_pattern this repository already declares must survive the screen", pattern)
		}
		for _, name := range append(append([]string{}, declared...), neighbours...) {
			if got, want := MatchBoundedName(name, pattern), name == pattern; got != want {
				t.Errorf("MatchBoundedName(%q, %q) = %v, want %v — equality is what a starless pattern meant", name, pattern, got, want)
			}
		}
	}
}
