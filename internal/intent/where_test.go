package intent

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// declarationProblems parses one inline declaration and returns what validation
// said about it — the shape every case below asserts against, because a where
// clause the author got wrong must come back as a named problem rather than as
// a component that quietly selects nothing.
func declarationProblems(t *testing.T, doc string) []string {
	t.Helper()
	var d Declaration
	if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
		return []string{"yaml: " + err.Error()}
	}
	d.Source = RepoFileName
	return d.Problems()
}

func TestWhere_ParsesIntoAPredicateAndAKindNarrowing(t *testing.T) {
	var d Declaration
	doc := `
components:
  - name: models
    where: { kind: storage, storage_kind: model, framework: rails }
`
	if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
		t.Fatal(err)
	}
	c := d.Components[0]
	if got := c.FactKind(); got != "storage" {
		t.Errorf("FactKind = %q, want storage — the reserved key is the kind narrowing, not a property test", got)
	}
	predicate := c.Predicate()
	want := map[string]string{"storage_kind": "model", "framework": "rails"}
	if len(predicate) != len(want) {
		t.Fatalf("Predicate = %v, want %v (kind lifted out)", predicate, want)
	}
	for prop, value := range want {
		if predicate[prop] != value {
			t.Errorf("Predicate[%s] = %q, want %q", prop, predicate[prop], value)
		}
	}
	if got := EncodeWhere(predicate); got != "framework=rails storage_kind=model" {
		t.Errorf("EncodeWhere = %q, want the pairs sorted by property so the compiled fact is a function of the SET", got)
	}
}

func TestWhere_EncodeDecodeRoundTripsIncludingValuesCarryingEquals(t *testing.T) {
	predicate := map[string]string{"local_types": "page_publication=PagePublication", "framework": "rails"}
	decoded := DecodeWhere(EncodeWhere(predicate))
	want := []WherePair{
		{Prop: "framework", Value: "rails"},
		{Prop: "local_types", Value: "page_publication=PagePublication"},
	}
	if len(decoded) != len(want) {
		t.Fatalf("DecodeWhere = %+v, want %+v", decoded, want)
	}
	for i := range want {
		if decoded[i] != want[i] {
			t.Errorf("DecodeWhere[%d] = %+v, want %+v — the FIRST = separates, so a value may carry more", i, decoded[i], want[i])
		}
	}
}

func TestWhere_ScalarKindsRenderCanonically(t *testing.T) {
	var d Declaration
	doc := `
components:
  - name: hairy
    where: { cyclomatic: 10, exported: true, ratio: 2.5 }
`
	if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
		t.Fatal(err)
	}
	predicate := d.Components[0].Predicate()
	for prop, want := range map[string]string{"cyclomatic": "10", "exported": "true", "ratio": "2.5"} {
		if predicate[prop] != want {
			t.Errorf("Predicate[%s] = %q, want %q — a YAML number must meet a measured number as the same token", prop, predicate[prop], want)
		}
	}
}

func TestWhere_ValidationRejectsWhatTheEvaluatorWouldMisread(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want string
	}{
		"a malformed comparator is named, never compared as a literal": {
			doc: `
components:
  - name: hairy
    where: { cyclomatic: ">=lots" }
`,
			want: "is not a numeric threshold",
		},
		"a doubled comparator is named": {
			doc: `
components:
  - name: hairy
    where: { cyclomatic: ">>5" }
`,
			want: "is not a numeric threshold",
		},
		"a comparator with no number is named": {
			doc: `
components:
  - name: hairy
    where: { cyclomatic: ">=" }
`,
			want: "is not a numeric threshold",
		},
		"a value carrying whitespace is refused": {
			doc: `
components:
  - name: wide
    where: { columns: "id company_id" }
`,
			want: "must carry no whitespace",
		},
		"a list value is not a property test": {
			doc: `
components:
  - name: wide
    where:
      columns: [id, company_id]
`,
			want: "must be a scalar value",
		},
		"a key outside the property character set is refused": {
			doc: `
components:
  - name: wide
    where: { "Storage-Kind": model }
`,
			want: "must be a fact property name",
		},
		"an empty predicate narrows nothing": {
			doc: `
components:
  - name: wide
    where: {}
`,
			want: "where needs at least one property pair",
		},
		"the reserved kind key takes only measured fact kinds": {
			doc: `
components:
  - name: wide
    where: { kind: table }
`,
			want: "is not a measured fact kind",
		},
		"kind declared in both spellings is refused rather than resolved": {
			doc: `
components:
  - name: wide
    kind: symbol
    where: { kind: storage }
`,
			want: "kind is declared twice",
		},
		"an empty value selects nothing and says so": {
			doc: `
components:
  - name: wide
    where: { framework: "" }
`,
			want: "has an empty value",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			problems := declarationProblems(t, tc.doc)
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems = %v, want one containing %q", problems, tc.want)
			}
		})
	}
}

// A where alone is a complete selector: the predicate IS the narrowing, so the
// "needs a match pattern" requirement must not fire on it — and must still fire
// on a component that declares no selector at all.
func TestWhere_IsASelectorInItsOwnRight(t *testing.T) {
	problems := declarationProblems(t, `
components:
  - name: view-components
    where: { superclass: "ViewComponent::Base" }
rules:
  - id: components-are-capped
    cap: view-components
    max_members: 400
    because: "The presentation surface is bounded by decision, not by drift."
`)
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none: a where predicate is a selector", problems)
	}
	problems = declarationProblems(t, `
components:
  - name: nothing
`)
	if !strings.Contains(strings.Join(problems, "\n"), "needs at least one match pattern, a service, or a where predicate") {
		t.Errorf("problems = %v, want the selector requirement to name where as one of the three", problems)
	}
}

func TestParseThreshold_GrammarIsOneComparatorAndOneNumber(t *testing.T) {
	cases := map[string]struct {
		op string
		n  float64
		ok bool
	}{
		">=5":   {op: ">=", n: 5, ok: true},
		"<=2.5": {op: "<=", n: 2.5, ok: true},
		">0":    {op: ">", n: 0, ok: true},
		"<-3":   {op: "<", n: -3, ok: true},
		">=":    {ok: false},
		">=x":   {ok: false},
		">>5":   {ok: false},
		"5":     {ok: false},
		"rails": {ok: false},
	}
	for value, want := range cases {
		op, n, ok := ParseThreshold(value)
		if ok != want.ok {
			t.Errorf("ParseThreshold(%q) ok = %v, want %v", value, ok, want.ok)
			continue
		}
		if ok && (op != want.op || n != want.n) {
			t.Errorf("ParseThreshold(%q) = (%q, %v), want (%q, %v)", value, op, n, want.op, want.n)
		}
	}
}

func TestCompileFacts_ComponentCarriesTheEncodedPredicate(t *testing.T) {
	d := &Declaration{
		Source: RepoFileName,
		Components: []ConstraintComponent{{
			Name:  "models",
			Where: map[string]any{"kind": "storage", "storage_kind": "model"},
		}},
	}
	compiled := CompileFacts(d)
	if len(compiled) != 1 {
		t.Fatalf("compiled = %d facts, want 1", len(compiled))
	}
	props := compiled[0].Props
	if props["kind"] != "storage" {
		t.Errorf("kind prop = %v, want storage: both spellings compile to the one kind narrowing", props["kind"])
	}
	if props["where"] != "storage_kind=model" {
		t.Errorf("where prop = %v, want storage_kind=model (the reserved key lifted out)", props["where"])
	}
}

// A recipe role bound to a predicate is the whole point of recipes meeting
// concept selectors: one law, instantiated against a concept rather than a
// directory.
func TestRecipes_RoleBoundToAPredicate(t *testing.T) {
	recipes := []Recipe{{
		Path:  RecipesDirName + "/naming.yaml",
		Name:  "named-surface",
		Roles: []RecipeRole{{Name: "surface"}},
		Rules: []ConstraintRule{{
			ID:          "surface-is-named",
			RequireName: "surface",
			Pattern:     "*Error",
			Because:     "The suffix is the contract.",
		}},
	}}
	files := []ConstraintsFile{{
		Path: ConstraintsDirName + "/errors.yaml",
		UseRecipe: []RecipeInstantiation{{
			Recipe: "named-surface",
			As:     "exceptions",
			Bind: map[string]RecipeBinding{
				"surface": {Where: map[string]any{"superclass": "StandardError"}},
			},
		}},
	}}
	components, rules, problems := ExpandInstantiations(files, recipes)
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if len(components) != 1 || components[0].Name != "exceptions/surface" {
		t.Fatalf("components = %+v, want one named exceptions/surface", components)
	}
	if got := components[0].Predicate()["superclass"]; got != "StandardError" {
		t.Errorf("bound predicate = %q, want StandardError — the binding carries the where through", got)
	}
	if len(rules) != 1 || rules[0].ID != "exceptions/surface-is-named" {
		t.Fatalf("rules = %+v, want the one expanded rule", rules)
	}
	if problems := constraintProblems(components, rules); len(problems) != 0 {
		t.Errorf("expanded declaration problems = %v, want none", problems)
	}
}
