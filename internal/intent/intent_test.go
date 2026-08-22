package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_ValidDeclaration(t *testing.T) {
	d, err := Parse([]byte(`
service:
  name: payments
consumes:
  - repo: billing
    via: http-client
  - repo: analytics
    via: graphql
serves:
  - via: http
layers:
  - name: handlers
    paths: ["app/controllers/**"]
  - name: domain
    paths: ["app/models/**"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Service.Name != "payments" || len(d.Consumes) != 2 || len(d.Layers) != 2 {
		t.Fatalf("parsed = %+v", d)
	}
}

func TestParse_FreeFormViaIsAnError(t *testing.T) {
	_, err := Parse([]byte("consumes:\n  - repo: billing\n    via: rest\n"))
	if err == nil {
		t.Fatal("a via the linker does not define must be a parse error")
	}
	if !strings.Contains(err.Error(), "graphql") || !strings.Contains(err.Error(), "http-client") {
		t.Fatalf("the error must name the allowed set, got: %v", err)
	}
}

func TestParse_LayerShapeValidated(t *testing.T) {
	if _, err := Parse([]byte("layers:\n  - name: handlers\n")); err == nil {
		t.Fatal("a layer without paths must be a parse error")
	}
	if _, err := Parse([]byte("layers:\n  - paths: [\"a/**\"]\n")); err == nil {
		t.Fatal("a layer without a name must be a parse error")
	}
}

func TestLoadRepoFile_MissingIsNil(t *testing.T) {
	d, err := LoadRepoFile(t.TempDir())
	if err != nil || d != nil {
		t.Fatalf("missing file = (%v, %v), want (nil, nil)", d, err)
	}
}

func TestLoadRepoFile_InvalidIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RepoFileName), []byte("consumes:\n  - repo: x\n    via: bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRepoFile(dir); err == nil {
		t.Fatal("a present-but-invalid declaration must error, never silently skip")
	}
}

func TestResolve_ClusterOverridesWholesale(t *testing.T) {
	file := &Declaration{Consumes: []Seam{{Repo: "a", Via: "http"}, {Repo: "b", Via: "kafka"}}, Source: "repo/enola-intent.yaml"}
	cluster := &Declaration{Consumes: []Seam{{Repo: "c", Via: "graphql"}}}
	got := Resolve(file, cluster)
	if !got.Overridden || got.Source != ClusterSource {
		t.Fatalf("override not recorded: %+v", got)
	}
	if len(got.Consumes) != 1 || got.Consumes[0].Repo != "c" {
		t.Fatalf("override must be wholesale, never key-merged: %+v", got.Consumes)
	}
	if only := Resolve(file, nil); only != file || only.Overridden {
		t.Fatalf("file-only resolution changed the declaration: %+v", only)
	}
	if only := Resolve(nil, cluster); only.Overridden || only.Source != ClusterSource {
		t.Fatalf("cluster-only resolution mis-recorded: %+v", only)
	}
}

func constraintDecl(components []ConstraintComponent, rules []ConstraintRule) *Declaration {
	return &Declaration{Components: components, Rules: rules}
}

func TestDeclarationValidate_ConstraintVocabulary(t *testing.T) {
	good := ConstraintComponent{Name: "domain", Match: []string{"app/domain/**", "lib/pricing"}}
	adapters := ConstraintComponent{Name: "adapters", Match: []string{"app/adapters/**"}}
	rule := ConstraintRule{ID: "domain-stays-pure", Forbid: "domain", To: "adapters", Via: "depends_on", Because: "the domain must not know its delivery mechanisms"}
	if err := constraintDecl([]ConstraintComponent{good, adapters}, []ConstraintRule{rule}).Validate(); err != nil {
		t.Fatalf("a well-formed constraint block must validate, got: %v", err)
	}
	for name, tc := range map[string]struct {
		components []ConstraintComponent
		rules      []ConstraintRule
		wantIn     string
	}{
		"free glob rejected": {
			[]ConstraintComponent{{Name: "domain", Match: []string{"app/*/domain"}}}, nil, "prefix/**"},
		"component kind vocabulary is closed": {
			[]ConstraintComponent{{Name: "domain", Match: []string{"app/domain/**"}, Kind: "class"}}, nil, "module, route, storage, symbol"},
		"matchless component rejected": {
			[]ConstraintComponent{{Name: "domain"}}, nil, "at least one match"},
		"service must be a lowercase token": {
			[]ConstraintComponent{{Name: "billing-internal", Service: "Billing"}}, nil, "lowercase token"},
		"via vocabulary is closed": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "uses", Because: "x"}}, "calls, depends_on, implements, imports"},
		"undeclared component rejected": {
			[]ConstraintComponent{good},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls", Because: "x"}}, "names no declared component"},
		"duplicate rule id rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{rule, rule}, "declared twice"},
		"missing because rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls"}}, "because"},
		"two forms rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls", Cap: "domain", MaxMembers: 3, Because: "x"}}, "exactly one"},
		"formless rule rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Via: "calls", Because: "x"}}, "exactly one"},
		"allow without only rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Allow: "domain", Via: "calls", Because: "x"}}, "at least one only"},
		"undeclared only component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Allow: "domain", Only: []string{"ghost"}, Via: "calls", Because: "x"}}, "names no declared component"},
		"cap without positive max rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Cap: "domain", Because: "x"}}, "at least 1"},
		"mode vocabulary is closed": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls", Mode: "loose", Because: "x"}}, "advisory, ratchet, strict"},
		"via rejected off the edge forms": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", ForbidFact: "domain", Via: "calls", Because: "x"}}, "edge forms"},
		"to rejected off the forbid form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Allow: "domain", Only: []string{"adapters"}, To: "adapters", Via: "calls", Because: "x"}}, "to belongs"},
		"forbid_reach without to rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", ForbidReach: "domain", Because: "x"}}, "forbid_reach needs a to"},
		"forbid_reach via vocabulary is closed": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", ForbidReach: "domain", To: "adapters", Via: "uses", Because: "x"}}, "calls, depends_on, implements, imports"},
		"forbid_reach undeclared component rejected": {
			[]ConstraintComponent{good},
			[]ConstraintRule{{ID: "r", ForbidReach: "domain", To: "adapters", Because: "x"}}, "names no declared component"},
		"protect without owners rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protect: "domain", Via: "calls", Because: "x"}}, "at least one owners"},
		"undeclared owners component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protect: "domain", Owners: []string{"ghost"}, Via: "calls", Because: "x"}}, "names no declared component"},
		"owners rejected off the protect form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Owners: []string{"adapters"}, Via: "calls", Because: "x"}}, "owners belongs"},
		"undeclared except component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Private: "domain", Except: []string{"ghost"}, Because: "x"}}, "names no declared component"},
		"except rejected off the private form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Except: []string{"adapters"}, Via: "calls", Because: "x"}}, "except belongs"},
		"via rejected on the private form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Private: "domain", Via: "calls", Because: "x"}}, "edge forms"},
		"forbid_name with a bad pattern rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", ForbidName: "domain", Pattern: "get_*_total", Because: "x"}}, "forbid_name needs a pattern"},
		"forbid_name surface other than exported rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", ForbidName: "domain", Pattern: "get_*", Surface: "public", Because: "x"}}, "surface must be exported"},
		"surface rejected off the forbid_name form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireName: "domain", Pattern: "*Job", Surface: "exported", Because: "x"}}, "surface belongs"},
		"require_defines without method rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireDefines: "domain", Because: "x"}}, "needs a method"},
		"whitespace method rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireDefines: "domain", Method: "per form", Because: "x"}}, "needs a method"},
		"method rejected off the require_defines form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Method: "perform", Via: "calls", Because: "x"}}, "method belongs"},
		"undeclared require_defines component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireDefines: "ghost", Method: "perform", Because: "x"}}, "names no declared component"},
		"require_name without pattern rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireName: "domain", Because: "x"}}, "exact name, a prefix*, or a *suffix"},
		"general glob pattern rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireName: "domain", Pattern: "Admin*Job", Because: "x"}}, "exact name, a prefix*, or a *suffix"},
		"bare star pattern rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireName: "domain", Pattern: "*", Because: "x"}}, "exact name, a prefix*, or a *suffix"},
		"pattern rejected off the require_name form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Pattern: "*Job", Via: "calls", Because: "x"}}, "pattern belongs"},
		"require_edge without direction rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", To: "adapters", Via: "calls", Because: "x"}}, "inbound, outbound"},
		"require_edge direction vocabulary is closed": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", To: "adapters", Via: "calls", Direction: "sideways", Because: "x"}}, "inbound, outbound"},
		"require_edge via vocabulary is closed": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", To: "adapters", Via: "uses", Direction: "inbound", Because: "x"}}, "calls, depends_on, implements, imports"},
		"require_edge without via rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", To: "adapters", Direction: "inbound", Because: "x"}}, "calls, depends_on, implements, imports"},
		"require_edge plus require rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", Via: "calls", Direction: "inbound", Require: "domain", MustPropContain: &PropMatch{Prop: "columns", Value: "id"}, Because: "x"}}, "exactly one"},
		"undeclared require_edge component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "ghost", Via: "calls", Direction: "inbound", Because: "x"}}, "names no declared component"},
		"undeclared to on require_edge rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", To: "ghost", Via: "calls", Direction: "inbound", Because: "x"}}, "names no declared component"},
		"to_name off the forbid form rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", Via: "calls", Direction: "outbound", ToName: []string{"*.task"}, Because: "x"}}, "to_name belongs to the forbid form"},
		"to and to_name together rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", ToName: []string{"*.task"}, Via: "calls", Because: "x"}}, "declare exactly one"},
		"forbid with neither far end rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", Via: "calls", Because: "x"}}, "forbid needs a far end"},
		"to_name speaks the bounded dialect": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", ToName: []string{"*mid*"}, Via: "calls", Because: "x"}}, "must be a literal edge target"},
		"to_name carries no whitespace": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", ToName: []string{"two names"}, Via: "calls", Because: "x"}}, "must carry no whitespace"},
		"when_edge_to on require_edge needs a when_via": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", To: "adapters", Via: "calls", Direction: "outbound", WhenEdgeTo: []string{"*.load"}, Because: "x"}}, "needs a when_via"},
		"when_via off the require_edge form rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Require: "domain", WhenEdgeTo: []string{"*.load"}, Via: "calls", WhenVia: "calls", MustPropContain: &PropMatch{Prop: "columns", Value: "id"}, Because: "x"}}, "when_via belongs to the require_edge form"},
		"when_via without an antecedent rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", To: "adapters", Via: "calls", Direction: "outbound", WhenVia: "calls", Because: "x"}}, "no antecedent is declared"},
		"when_via vocabulary is closed": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", RequireEdge: "domain", To: "adapters", Via: "calls", Direction: "outbound", WhenEdgeTo: []string{"*.load"}, WhenVia: "uses", Because: "x"}}, "calls, depends_on, implements, imports"},
		"when_edge_to off the require and require_edge forms rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls", WhenEdgeTo: []string{"*.load"}, Because: "x"}}, "when_edge_to belongs to the require and require_edge forms"},
		"direction rejected off the require_edge form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls", Direction: "inbound", Because: "x"}}, "direction belongs"},
		"protocol with one step rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protocol: "domain", Steps: []string{"adapters"}, Via: "calls", Because: "x"}}, "at least 2 steps"},
		"duplicate protocol step rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protocol: "domain", Steps: []string{"adapters", "adapters"}, Via: "calls", Because: "x"}}, "appears twice in the declared order"},
		"undeclared step component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protocol: "domain", Steps: []string{"adapters", "ghost"}, Via: "calls", Because: "x"}}, "names no declared component"},
		"undeclared protocol component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protocol: "ghost", Steps: []string{"domain", "adapters"}, Via: "calls", Because: "x"}}, "names no declared component"},
		"protocol via vocabulary is closed": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protocol: "domain", Steps: []string{"domain", "adapters"}, Via: "uses", Because: "x"}}, "calls, depends_on, implements, imports"},
		"protocol without via rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protocol: "domain", Steps: []string{"domain", "adapters"}, Because: "x"}}, "calls, depends_on, implements, imports"},
		"steps rejected off the protocol form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Steps: []string{"adapters"}, Via: "calls", Because: "x"}}, "steps belongs"},
		"protocol plus a law form rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Protocol: "domain", Steps: []string{"domain", "adapters"}, Via: "calls", Cap: "domain", MaxMembers: 3, Because: "x"}}, "exactly one"},
		"require without must rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Require: "domain", Because: "x"}}, "must_prop_contain"},
		"half a when clause rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Require: "domain", MustPropContain: &PropMatch{Prop: "columns", Value: "id"}, WhenPropContains: &PropMatch{Prop: "columns"}, Because: "x"}}, "both prop and value"},
		"undeclared require component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Require: "ghost", MustPropContain: &PropMatch{Prop: "columns", Value: "id"}, Because: "x"}}, "names no declared component"},
		"via rejected on the require form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Require: "domain", MustPropContain: &PropMatch{Prop: "columns", Value: "id"}, Via: "calls", Because: "x"}}, "edge forms"},
		"require clauses rejected off the require form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls", MustPropContain: &PropMatch{Prop: "columns", Value: "id"}, Because: "x"}}, "belong to the require form"},
		"guide without message rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Guide: "domain", Because: "x"}}, "needs a message"},
		"whitespace exemplar rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Guide: "domain", Message: "consider @cached", Exemplars: []string{"app/a.js", "app/b c.js"}, Because: "x"}}, "whitespace-free"},
		"undeclared guide component rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Guide: "ghost", Message: "consider @cached", Because: "x"}}, "names no declared component"},
		"enforce-class mode rejected on guidance": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Guide: "domain", Message: "consider @cached", Mode: "ratchet", Because: "x"}}, "advisory, notify"},
		"strict mode rejected on guidance": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Guide: "domain", Message: "consider @cached", Mode: "strict", Because: "x"}}, "writing a law form"},
		"notify rejected on a law form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls", Mode: "notify", Because: "x"}}, "advisory, ratchet, strict"},
		"message rejected off the guide form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Forbid: "domain", To: "adapters", Via: "calls", Message: "consider @cached", Because: "x"}}, "belong to the guide form"},
		"guide plus a law form rejected": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Guide: "domain", Message: "consider @cached", Cap: "domain", MaxMembers: 3, Because: "x"}}, "exactly one"},
		"via rejected on the guide form": {
			[]ConstraintComponent{good, adapters},
			[]ConstraintRule{{ID: "r", Guide: "domain", Message: "consider @cached", Via: "calls", Because: "x"}}, "edge forms"},
	} {
		t.Run(name, func(t *testing.T) {
			err := constraintDecl(tc.components, tc.rules).Validate()
			if err == nil {
				t.Fatal("an ill-formed constraint block must be a validation error")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("the error must name what is allowed; got: %v", err)
			}
		})
	}
}

func TestDeclarationValidate_ServiceScopedComponents(t *testing.T) {
	whole := ConstraintComponent{Name: "frontend", Service: "frontend"}
	narrowed := ConstraintComponent{Name: "billing-internal", Service: "billing", Match: []string{"internal/**"}, Kind: "symbol"}
	rule := ConstraintRule{ID: "no-internal-reach", Forbid: "frontend", To: "billing-internal", Via: "calls", Because: "internal surfaces are not a contract"}
	if err := constraintDecl([]ConstraintComponent{whole, narrowed}, []ConstraintRule{rule}).Validate(); err != nil {
		t.Fatalf("a service-scoped component needs no match patterns, got: %v", err)
	}
}

func TestCompileFacts_ServiceScopedComponent(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{{Name: "billing-internal", Service: "billing", Match: []string{"internal/**"}}},
		nil,
	)
	decl.Source = RepoFileName
	ff := CompileFacts(decl)
	if len(ff) != 1 {
		t.Fatalf("facts = %d, want 1: %+v", len(ff), ff)
	}
	if got := ff[0].PropString("service"); got != "billing" {
		t.Errorf("service prop = %q, want billing", got)
	}
	if got := ff[0].PropString("match"); got != "internal/**" {
		t.Errorf("match prop = %q", got)
	}
}

func TestCompileFacts_ComponentsAndRules(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{{Name: "domain", Match: []string{"lib/pricing", "app/domain/**"}, Kind: "module", NamePattern: "app/domain/billing"}},
		[]ConstraintRule{{ID: "domain-stays-pure", Forbid: "domain", To: "adapters", Via: "depends_on", Because: "why"}},
	)
	decl.Source = RepoFileName
	ff := CompileFacts(decl)
	if len(ff) != 2 {
		t.Fatalf("facts = %d, want 2: %+v", len(ff), ff)
	}
	comp, rule := ff[0], ff[1]
	if comp.Name != "component: domain" || comp.File != RepoFileName {
		t.Errorf("component fact = %+v", comp)
	}
	if comp.PropString("source") != RepoFileName || rule.PropString("source") != RepoFileName {
		t.Errorf("constraint facts must carry the declaration's provenance, got %+v / %+v", comp.Props, rule.Props)
	}
	// Sorted regardless of declaration order, so the compiled fact fingerprints
	// the declared SET.
	if got := comp.PropString("match"); got != "app/domain/** lib/pricing" {
		t.Errorf("match prop = %q, want the sorted join", got)
	}
	if comp.PropString("kind") != "module" || comp.PropString("name_pattern") != "app/domain/billing" {
		t.Errorf("component props = %+v", comp.Props)
	}
	if rule.Name != "rule: domain-stays-pure" ||
		rule.PropString("forbid") != "domain" || rule.PropString("to") != "adapters" ||
		rule.PropString("via") != "depends_on" || rule.PropString("because") != "why" {
		t.Errorf("rule fact = %+v", rule)
	}
	// An absent mode and an explicit ratchet declare the same enforcement, so
	// they must fingerprint the same.
	if rule.PropString("mode") != "ratchet" {
		t.Errorf("mode = %q, want the ratchet default", rule.PropString("mode"))
	}
}

func TestCompileFacts_RuleForms(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{
			{Name: "domain", Match: []string{"app/domain/**"}},
			{Name: "contracts", Match: []string{"app/contracts/**"}},
			{Name: "events", Match: []string{"app/events/**"}},
		},
		[]ConstraintRule{
			{ID: "allow-rule", Allow: "domain", Only: []string{"events", "contracts"}, Via: "imports", Mode: "advisory", Because: "why"},
			{ID: "forbid-fact-rule", ForbidFact: "domain", Because: "why"},
			{ID: "cap-rule", Cap: "domain", MaxMembers: 5, Because: "why"},
		},
	)
	ff := CompileFacts(decl)
	if len(ff) != 6 {
		t.Fatalf("facts = %d, want 3 components + 3 rules: %+v", len(ff), ff)
	}
	allow, forbidFact, cap := ff[3], ff[4], ff[5]
	if allow.PropString("allow") != "domain" || allow.PropString("via") != "imports" {
		t.Errorf("allow fact = %+v", allow.Props)
	}
	// Sorted regardless of declaration order, like a component's match set.
	if got := allow.PropString("only"); got != "contracts events" {
		t.Errorf("only prop = %q, want the sorted join", got)
	}
	if allow.PropString("mode") != "advisory" {
		t.Errorf("mode = %q, want the declared advisory", allow.PropString("mode"))
	}
	if forbidFact.PropString("forbid_fact") != "domain" || forbidFact.PropString("via") != "" {
		t.Errorf("forbid-fact fact = %+v", forbidFact.Props)
	}
	if cap.PropString("cap") != "domain" {
		t.Errorf("cap fact = %+v", cap.Props)
	}
	if got, ok := cap.Props["max_members"].(int); !ok || got != 5 {
		t.Errorf("max_members = %v, want 5", cap.Props["max_members"])
	}
}

func TestCompileFacts_ForbidReachRule(t *testing.T) {
	vialess := constraintDecl(
		[]ConstraintComponent{
			{Name: "domain", Match: []string{"app/domain/**"}},
			{Name: "adapters", Match: []string{"app/adapters/**"}},
		},
		[]ConstraintRule{{ID: "no-reach", ForbidReach: "domain", To: "adapters", Because: "why"}},
	)
	if err := vialess.Validate(); err != nil {
		t.Fatalf("a via-less forbid_reach rule must validate — the default is every rule-via kind — got: %v", err)
	}
	ff := CompileFacts(vialess)
	rule := ff[len(ff)-1]
	if rule.PropString("forbid_reach") != "domain" || rule.PropString("to") != "adapters" {
		t.Errorf("forbid_reach fact = %+v", rule.Props)
	}
	if _, present := rule.Props["via"]; present {
		t.Errorf("an absent via must compile no via prop: %+v", rule.Props)
	}

	narrowed := constraintDecl(
		[]ConstraintComponent{
			{Name: "domain", Match: []string{"app/domain/**"}},
			{Name: "adapters", Match: []string{"app/adapters/**"}},
		},
		[]ConstraintRule{{ID: "no-reach", ForbidReach: "domain", To: "adapters", Via: "calls", Because: "why"}},
	)
	if err := narrowed.Validate(); err != nil {
		t.Fatalf("a via-narrowed forbid_reach rule must validate, got: %v", err)
	}
	ff = CompileFacts(narrowed)
	rule = ff[len(ff)-1]
	if rule.PropString("via") != "calls" {
		t.Errorf("a declared via must compile: %+v", rule.Props)
	}
}

func TestCompileFacts_ProtectRule(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{
			{Name: "invoices", Match: []string{"db/**"}, Kind: "storage"},
			{Name: "billing", Match: []string{"app/billing/**"}},
			{Name: "audit", Match: []string{"app/audit/**"}},
		},
		[]ConstraintRule{{ID: "only-billing", Protect: "invoices", Owners: []string{"billing", "audit"}, Via: "depends_on", Because: "why"}},
	)
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("protect") != "invoices" || rule.PropString("via") != "depends_on" {
		t.Errorf("protect fact = %+v", rule.Props)
	}
	if got := rule.PropString("owners"); got != "audit billing" {
		t.Errorf("owners prop = %q, want the sorted join", got)
	}
}

func TestCompileFacts_PrivateRule(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{
			{Name: "billing", Match: []string{"app/billing/**"}},
			{Name: "support", Match: []string{"app/support/**"}},
			{Name: "audit", Match: []string{"app/audit/**"}},
		},
		[]ConstraintRule{{ID: "pack-internals", Private: "billing", Except: []string{"support", "audit"}, Because: "why"}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("a well-formed private rule must validate, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("private") != "billing" {
		t.Errorf("private fact = %+v", rule.Props)
	}
	if got := rule.PropString("except"); got != "audit support" {
		t.Errorf("except prop = %q, want the sorted join", got)
	}
	noExcept := constraintDecl(
		[]ConstraintComponent{{Name: "billing", Match: []string{"app/billing/**"}}},
		[]ConstraintRule{{ID: "r", Private: "billing", Because: "x"}},
	)
	ff = CompileFacts(noExcept)
	rule = ff[len(ff)-1]
	if _, present := rule.Props["except"]; present {
		t.Errorf("an absent except must compile no except prop: %+v", rule.Props)
	}
}

func TestCompileFacts_RequireDefinesRule(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{{Name: "jobs", Match: []string{"app/jobs/**"}, Kind: "symbol"}},
		[]ConstraintRule{{ID: "jobs-perform", RequireDefines: "jobs", Method: "perform", Because: "why"}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("a well-formed require_defines rule must validate, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("require_defines") != "jobs" || rule.PropString("method") != "perform" {
		t.Errorf("require_defines fact = %+v", rule.Props)
	}
}

func TestCompileFacts_RequireNameRule(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{{Name: "jobs", Match: []string{"app/jobs/**"}, Kind: "symbol"}},
		[]ConstraintRule{{ID: "jobs-named-job", RequireName: "jobs", Pattern: "*Job", Because: "why"}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("a well-formed require_name rule must validate, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("require_name") != "jobs" || rule.PropString("pattern") != "*Job" {
		t.Errorf("require_name fact = %+v", rule.Props)
	}
}

func TestCompileFacts_RequireEdgeRule(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{
			{Name: "events", Match: []string{"app/events/**"}, Kind: "symbol"},
			{Name: "handlers", Match: []string{"app/handlers/**"}},
		},
		[]ConstraintRule{{ID: "every-event-consumed", RequireEdge: "events", To: "handlers", Via: "calls", Direction: "inbound", Because: "why"}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("a well-formed require_edge rule must validate, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("require_edge") != "events" || rule.PropString("to") != "handlers" ||
		rule.PropString("via") != "calls" || rule.PropString("direction") != "inbound" {
		t.Errorf("require_edge fact = %+v", rule.Props)
	}
}

func TestCompileFacts_RequireEdgeRuleWithoutTo(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{{Name: "events", Match: []string{"app/events/**"}}},
		[]ConstraintRule{{ID: "events-consumed-somewhere", RequireEdge: "events", Via: "calls", Direction: "inbound", Because: "why"}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("to is optional on require_edge — omitting it means from anywhere, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if _, present := rule.Props["to"]; present {
		t.Errorf("an absent to must not compile a prop: %+v", rule.Props)
	}
}

func TestCompileFacts_ProtocolRuleKeepsStepOrderAndCarriesTheStructuralLevel(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{
			{Name: "checkout-callers", Match: []string{"app/checkout/**"}},
			{Name: "validate-cart", Match: []string{"app/steps/validate/**"}},
			{Name: "reserve-stock", Match: []string{"app/steps/reserve/**"}},
			{Name: "charge-payment", Match: []string{"app/steps/charge/**"}},
		},
		[]ConstraintRule{{ID: "checkout-protocol", Protocol: "checkout-callers",
			Steps: []string{"validate-cart", "reserve-stock", "charge-payment"}, Via: "calls", Because: "why"}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("a well-formed protocol rule must validate, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("protocol") != "checkout-callers" || rule.PropString("via") != "calls" {
		t.Errorf("protocol fact = %+v", rule.Props)
	}
	if rule.PropString("steps") != "validate-cart reserve-stock charge-payment" {
		t.Errorf("steps = %q: the declared order is the rule's semantic content and must never be sorted", rule.PropString("steps"))
	}
	if rule.PropString("verification") != "structural" {
		t.Errorf("verification = %q, want structural — the only level a static graph can honestly claim", rule.PropString("verification"))
	}
}

func TestCompileFacts_GuideRule(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{{Name: "components", Match: []string{"app/components/**"}}},
		[]ConstraintRule{{
			ID:        "getters-cached",
			Guide:     "components",
			Message:   "Expensive derived getters here use @cached — consider it (see exemplars)",
			Exemplars: []string{"app/components/sortable-table.js", "app/components/avatar-stack.js"},
			Because:   "recomputing derived state on every render is the recurring perf bug here",
		}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("a well-formed guide rule must validate, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("guide") != "components" {
		t.Errorf("guide fact = %+v", rule.Props)
	}
	if got := rule.PropString("message"); got != "Expensive derived getters here use @cached — consider it (see exemplars)" {
		t.Errorf("message prop = %q", got)
	}
	if got := rule.PropString("exemplars"); got != "app/components/avatar-stack.js app/components/sortable-table.js" {
		t.Errorf("exemplars must compile sorted, got %q", got)
	}
	if got := rule.PropString("mode"); got != "notify" {
		t.Errorf("mode = %q, want notify — guidance's default is the quiet channel", got)
	}
	advisory := constraintDecl(
		[]ConstraintComponent{{Name: "components", Match: []string{"app/components/**"}}},
		[]ConstraintRule{{ID: "g", Guide: "components", Message: "consider @cached", Mode: "advisory", Because: "x"}},
	)
	ff = CompileFacts(advisory)
	rule = ff[len(ff)-1]
	if got := rule.PropString("mode"); got != "advisory" {
		t.Errorf("mode = %q, want advisory to survive compilation", got)
	}
	if _, present := rule.Props["exemplars"]; present {
		t.Errorf("absent exemplars must compile no prop: %+v", rule.Props)
	}
}

func TestCompileFacts_RequireRule(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{{Name: "tables", Match: []string{"db/**"}, Kind: "storage"}},
		[]ConstraintRule{{
			ID:               "company-fk",
			Require:          "tables",
			WhenPropContains: &PropMatch{Prop: "columns", Value: "company_id"},
			MustPropContain:  &PropMatch{Prop: "fk_constraints", Value: "company_id->companies"},
			Because:          "tenant isolation joins through companies",
		}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("a well-formed require rule must validate, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("require") != "tables" ||
		rule.PropString("when_prop") != "columns" || rule.PropString("when_value") != "company_id" ||
		rule.PropString("must_prop") != "fk_constraints" || rule.PropString("must_value") != "company_id->companies" {
		t.Errorf("require fact = %+v", rule.Props)
	}
	// The when clause is optional; its props must not compile as empty strings.
	noWhen := constraintDecl(
		[]ConstraintComponent{{Name: "tables", Match: []string{"db/**"}}},
		[]ConstraintRule{{ID: "r", Require: "tables", MustPropContain: &PropMatch{Prop: "columns", Value: "id"}, Because: "x"}},
	)
	ff = CompileFacts(noWhen)
	rule = ff[len(ff)-1]
	if _, present := rule.Props["when_prop"]; present {
		t.Errorf("an absent when clause must compile no when props: %+v", rule.Props)
	}
}

// Constraints ride the same YAML file as every other declaration section, so
// the whole path — parse, validate, compile — must work from the file's bytes,
// not only from structs a test assembled.
func TestParse_ConstraintsInDeclarationFile(t *testing.T) {
	d, err := Parse([]byte(`
components:
  - name: domain
    match: ["app/domain/**"]
  - name: adapters
    match: ["app/adapters/**"]
rules:
  - id: domain-stays-pure
    forbid: domain
    to: adapters
    via: depends_on
    because: the domain must not know its delivery mechanisms
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Components) != 2 || len(d.Rules) != 1 {
		t.Fatalf("parsed = %+v", d)
	}
	if _, err := Parse([]byte("components:\n  - name: domain\n")); err == nil {
		t.Fatal("a matchless component must be a parse error at the file level too")
	}
}

func TestParsePage_ConstraintsAreRejected(t *testing.T) {
	for name, block := range map[string]string{
		"components": "  components:\n    - {name: domain, match: [app/domain/**]}\n",
		"rules":      "  rules:\n    - {id: r, forbid: a, to: b, via: imports, because: x}\n",
	} {
		t.Run(name, func(t *testing.T) {
			src := "---\nenola_intent:\n" + block + "---\nbody\n"
			_, err := ParsePage([]byte(src))
			if err == nil {
				t.Fatal("a page carrying constraints must fail loudly, never silently lose its law")
			}
			if !strings.Contains(err.Error(), RepoFileName) {
				t.Fatalf("the error must say where constraints live now; got: %v", err)
			}
		})
	}
}

// A claim's compiled name is what a failed-claim finding is titled with, so an
// absent optional prefix must not leave a gap in it.
func TestClaimNames_OmitAbsentPrefixes(t *testing.T) {
	v := 99
	for _, tc := range []struct {
		name string
		in   Claim
		want string
	}{
		{"no prefixes", Claim{Metric: "fact-count", Repo: "api", Kind: "route", Value: &v},
			"claim: api route = 99"},
		{"name prefix", Claim{Metric: "fact-count", Repo: "api", Kind: "route", NamePrefix: "/api", Value: &v},
			"claim: api route /api = 99"},
		{"file prefix", Claim{Metric: "fact-count", Repo: "api", Kind: "symbol", FilePrefix: "app/", Value: &v},
			"claim: api symbol app/ = 99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ff := CompilePageFacts(&PageIntent{Claims: []Claim{tc.in}}, "wiki/p.md")
			if len(ff) != 1 || ff[0].Name != tc.want {
				t.Fatalf("name = %q, want %q", ff[0].Name, tc.want)
			}
		})
	}
}

// A well-formed forbid_name validates and compiles with its pattern and
// surface, the way require_name does.
func TestForbidName_ValidationMirrorsRequireName(t *testing.T) {
	decl := constraintDecl(
		[]ConstraintComponent{{Name: "domain", Match: []string{"app/domain/**"}, Kind: "symbol"}},
		[]ConstraintRule{{ID: "no-getters", ForbidName: "domain", Pattern: "get_*", Surface: "exported", Because: "a reader is a noun"}},
	)
	if err := decl.Validate(); err != nil {
		t.Fatalf("a well-formed forbid_name rule must validate, got: %v", err)
	}
	ff := CompileFacts(decl)
	rule := ff[len(ff)-1]
	if rule.PropString("forbid_name") != "domain" || rule.PropString("pattern") != "get_*" || rule.PropString("surface") != "exported" {
		t.Fatalf("compiled rule lost its fields: %v", rule.Props)
	}
}
