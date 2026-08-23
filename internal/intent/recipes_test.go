package intent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func writeRecipeRepo(t *testing.T, inline string, constraintsFiles, recipeFiles map[string]string) string {
	t.Helper()
	dir := writeConstraintsRepo(t, inline, constraintsFiles)
	if len(recipeFiles) > 0 {
		rdir := filepath.Join(dir, filepath.FromSlash(RecipesDirName))
		if err := os.MkdirAll(rdir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range recipeFiles {
			if err := os.WriteFile(filepath.Join(rdir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

const eventDrivenRecipe = `
recipe: event-driven
roles:
  - name: events
  - name: bus
  - name: handlers
rules:
  - id: events-consumed
    require_edge: events
    to: handlers
    via: calls
    direction: inbound
    because: "An event nobody consumes is dead weight."
  - id: only-bus-calls-handlers
    protect: handlers
    owners: [bus]
    via: calls
    because: "Handlers are reached through the bus, never directly."
  - id: events-are-named
    require_name: events
    pattern: "*Event"
    because: "The suffix is the contract."
`

const ordersInstantiation = `
use_recipe:
  - recipe: event-driven
    as: orders-events
    bind:
      events:   { match: ["app/events/orders/**"] }
      bus:      { match: ["app/lib/event_bus.rb"] }
      handlers: { match: ["app/handlers/orders/**"] }
`

const billingInstantiation = `
use_recipe:
  - recipe: event-driven
    as: billing-events
    bind:
      events:   { match: ["app/events/billing/**"] }
      bus:      { match: ["app/lib/event_bus.rb"] }
      handlers: { match: ["app/handlers/billing/**"] }
`

func TestLoadRepoFile_UseRecipeExpandsRolesAndRules(t *testing.T) {
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": ordersInstantiation},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ConstraintComponent{}
	for _, c := range d.Components {
		byName[c.Name] = c
	}
	events, ok := byName["orders-events/events"]
	if !ok {
		t.Fatalf("expanded components = %+v, want orders-events/events among them", d.Components)
	}
	if events.SourceFile != ConstraintsDirName+"/orders.yaml" || events.Recipe != "event-driven" ||
		events.Instance != "orders-events" || events.Role != "events" {
		t.Fatalf("expanded component provenance = %+v", events)
	}
	if len(events.Match) != 1 || events.Match[0] != "app/events/orders/**" {
		t.Fatalf("binding match = %v", events.Match)
	}
	byID := map[string]ConstraintRule{}
	for _, r := range d.Rules {
		byID[r.ID] = r
	}
	consumed, ok := byID["orders-events/events-consumed"]
	if !ok {
		t.Fatalf("expanded rules = %+v, want orders-events/events-consumed among them", d.Rules)
	}
	if consumed.RequireEdge != "orders-events/events" || consumed.To != "orders-events/handlers" {
		t.Fatalf("role references must be remapped to instance components: %+v", consumed)
	}
	if consumed.SourceFile != ConstraintsDirName+"/orders.yaml" || consumed.Recipe != "event-driven" || consumed.Instance != "orders-events" {
		t.Fatalf("expanded rule provenance = %+v", consumed)
	}
	protect := byID["orders-events/only-bus-calls-handlers"]
	if protect.Protect != "orders-events/handlers" || len(protect.Owners) != 1 || protect.Owners[0] != "orders-events/bus" {
		t.Fatalf("owners must be remapped per instance: %+v", protect)
	}
	named := byID["orders-events/events-are-named"]
	if named.RequireName != "orders-events/events" || named.Pattern != "*Event" {
		t.Fatalf("require_name expansion = %+v", named)
	}
}

const checkoutRecipe = `
recipe: checkout
roles:
  - name: callers
  - name: validate
  - name: reserve
  - name: charge
rules:
  - id: order
    protocol: callers
    steps: [validate, reserve, charge]
    via: calls
    because: "Charging without reserving oversells; reserving without validating reserves garbage."
`

const checkoutInstantiation = `
use_recipe:
  - recipe: checkout
    as: web-checkout
    bind:
      callers:  { match: ["app/checkout/**"] }
      validate: { match: ["app/steps/validate/**"] }
      reserve:  { match: ["app/steps/reserve/**"] }
      charge:   { match: ["app/steps/charge/**"] }
`

func TestLoadRepoFile_ProtocolStepsExpandPerInstanceInDeclaredOrder(t *testing.T) {
	dir := writeRecipeRepo(t, "",
		map[string]string{"checkout.yaml": checkoutInstantiation},
		map[string]string{"checkout.yaml": checkoutRecipe})
	recipes, loadProblems, err := LoadRecipesDir(dir)
	if err != nil || len(loadProblems) > 0 {
		t.Fatalf("load = (%v, %v)", loadProblems, err)
	}
	problems, warnings := RecipeProblems(recipes)
	if len(problems) != 0 || len(warnings) != 0 {
		t.Fatalf("step roles are referenced roles — no problems or dead-role warnings, got (%v, %v)", problems, warnings)
	}
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ConstraintRule{}
	for _, r := range d.Rules {
		byID[r.ID] = r
	}
	rule, ok := byID["web-checkout/order"]
	if !ok {
		t.Fatalf("expanded rules = %+v, want web-checkout/order among them", d.Rules)
	}
	if rule.Protocol != "web-checkout/callers" {
		t.Fatalf("protocol role must be remapped to the instance component: %+v", rule)
	}
	want := []string{"web-checkout/validate", "web-checkout/reserve", "web-checkout/charge"}
	if len(rule.Steps) != len(want) {
		t.Fatalf("steps = %v, want %v", rule.Steps, want)
	}
	for i := range want {
		if rule.Steps[i] != want[i] {
			t.Fatalf("steps = %v, want %v in the declared order", rule.Steps, want)
		}
	}
	byName := map[string]facts.Fact{}
	for _, f := range CompileFacts(d) {
		byName[f.Name] = f
	}
	compiled := byName["rule: web-checkout/order"]
	if compiled.PropString("steps") != "web-checkout/validate web-checkout/reserve web-checkout/charge" {
		t.Fatalf("compiled steps = %q, want the instance-prefixed declared order", compiled.PropString("steps"))
	}
	if compiled.PropString("verification") != "structural" {
		t.Fatalf("an expanded protocol rule keeps the structural verification level, got %q", compiled.PropString("verification"))
	}
}

func TestCompileFacts_ExpandedRulesCarryRecipeProvenance(t *testing.T) {
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": ordersInstantiation},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]facts.Fact{}
	for _, f := range CompileFacts(d) {
		byName[f.Name] = f
	}
	rule := byName["rule: orders-events/events-consumed"]
	if rule.Props["recipe"] != "event-driven" || rule.Props["instance"] != "orders-events" {
		t.Fatalf("rule fact props = %+v, want recipe and instance provenance", rule.Props)
	}
	if rule.File != ConstraintsDirName+"/orders.yaml" || rule.Props["source"] != ConstraintsDirName+"/orders.yaml" {
		t.Fatalf("rule fact must cite the instantiating file, got File=%q source=%v", rule.File, rule.Props["source"])
	}
	comp := byName["component: orders-events/events"]
	if comp.Props["recipe"] != "event-driven" || comp.Props["instance"] != "orders-events" || comp.Props["role"] != "events" {
		t.Fatalf("component fact props = %+v, want recipe, instance and role provenance", comp.Props)
	}
}

func TestLoadRepoFile_TwoInstancesExpandIndependently(t *testing.T) {
	orders := ordersInstantiation + `    exempt:
      - rule: events-consumed
        witness: "LegacyOrderMigratedEvent has no inbound calls edge from orders-events/handlers"
        owner: "dana"
        because: "Fired only by the migration backfill, consumed manually."
        since: "2026-08-11"
`
	dir := writeRecipeRepo(t, "",
		map[string]string{"billing.yaml": billingInstantiation, "orders.yaml": orders},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ConstraintRule{}
	for _, r := range d.Rules {
		byID[r.ID] = r
	}
	if len(byID) != 6 {
		t.Fatalf("rules = %d, want three per instance: %+v", len(byID), d.Rules)
	}
	ordersRule := byID["orders-events/events-consumed"]
	if len(ordersRule.Exempt) != 1 || ordersRule.Exempt[0].Owner != "dana" {
		t.Fatalf("the instance exemption must attach to its instance's rule: %+v", ordersRule.Exempt)
	}
	billingRule := byID["billing-events/events-consumed"]
	if len(billingRule.Exempt) != 0 {
		t.Fatalf("the other instance must not inherit the exemption: %+v", billingRule.Exempt)
	}
	if billingRule.RequireEdge != "billing-events/events" || ordersRule.RequireEdge != "orders-events/events" {
		t.Fatalf("each instance must reference its own components: %+v vs %+v", billingRule, ordersRule)
	}
}

func TestLoadRepoFile_MissingBindingForReferencedRoleFails(t *testing.T) {
	partial := `
use_recipe:
  - recipe: event-driven
    as: orders-events
    bind:
      events: { match: ["app/events/orders/**"] }
      bus:    { match: ["app/lib/event_bus.rb"] }
`
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": partial},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("an unbound role the rules reference must be an error")
	}
	if !strings.Contains(err.Error(), "handlers") || !strings.Contains(err.Error(), "binds no paths to it") ||
		!strings.Contains(err.Error(), ConstraintsDirName+"/orders.yaml") {
		t.Fatalf("the error must name the role and the instantiating file, got: %v", err)
	}
}

func TestLoadRepoFile_BindingUndeclaredRoleFails(t *testing.T) {
	extra := ordersInstantiation + `      queue: { match: ["app/jobs/**"] }
`
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": extra},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("binding a role the recipe does not declare must be an error")
	}
	if !strings.Contains(err.Error(), `bind "queue" names no role of recipe event-driven`) {
		t.Fatalf("the error must name the stray binding and the recipe's roles, got: %v", err)
	}
}

func TestLoadRepoFile_UnknownRecipeFails(t *testing.T) {
	// The instantiation names a recipe that neither this repository nor the
	// binary declares. `event-driven` would resolve, because it ships, so the
	// name here has to be one nothing answers to.
	instantiation := strings.Replace(ordersInstantiation, "recipe: event-driven", "recipe: no-such-arrangement", 1)
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": instantiation},
		map[string]string{"pipeline.yaml": strings.Replace(eventDrivenRecipe, "recipe: event-driven", "recipe: pipeline", 1)})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("instantiating a recipe nothing declares must be an error")
	}
	// The loaded set names what the repository authored beside what ships with
	// the binary, because both are instantiable and a reader looking for the
	// name they mistyped needs to see all of it.
	if !strings.Contains(err.Error(), `recipe "no-such-arrangement" names no loaded recipe (loaded: `) {
		t.Fatalf("the error must name the missing recipe and the loaded set, got: %v", err)
	}
	for _, loaded := range []string{"pipeline", "layered", "rails-conventions"} {
		if !strings.Contains(err.Error(), loaded) {
			t.Fatalf("the loaded set must name both what ships and what the repository authored, missing %q: %v", loaded, err)
		}
	}
}

func TestLoadRepoFile_DuplicateInstanceNamesAcrossFilesCiteBoth(t *testing.T) {
	dir := writeRecipeRepo(t, "",
		map[string]string{
			"a.yaml": ordersInstantiation,
			"b.yaml": ordersInstantiation,
		},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("one instance name in two constraints files must be an error")
	}
	if !strings.Contains(err.Error(), ConstraintsDirName+"/a.yaml") || !strings.Contains(err.Error(), ConstraintsDirName+"/b.yaml") ||
		!strings.Contains(err.Error(), "the expanded rule ids would collide") {
		t.Fatalf("the error must name both declaring files, got: %v", err)
	}
}

func TestLoadRepoFile_DuplicateRecipeNamesAcrossFilesCiteBoth(t *testing.T) {
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": ordersInstantiation},
		map[string]string{
			"a.yaml": eventDrivenRecipe,
			"b.yaml": eventDrivenRecipe,
		})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("one recipe name in two recipe files must be an error")
	}
	if !strings.Contains(err.Error(), RecipesDirName+"/a.yaml") || !strings.Contains(err.Error(), RecipesDirName+"/b.yaml") {
		t.Fatalf("the error must name both declaring files, got: %v", err)
	}
}

func TestLoadRepoFile_RecipeRuleReferencingUndeclaredRoleFails(t *testing.T) {
	broken := strings.Replace(eventDrivenRecipe, "require_edge: events", "require_edge: queue", 1)
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": ordersInstantiation},
		map[string]string{"event-driven.yaml": broken})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("a recipe rule referencing an undeclared role must be an error")
	}
	if !strings.Contains(err.Error(), `require_edge "queue" names no declared role`) ||
		!strings.Contains(err.Error(), RecipesDirName+"/event-driven.yaml") {
		t.Fatalf("the error must cite the recipe file and the missing role, got: %v", err)
	}
}

func TestLoadRepoFile_RecursiveRecipeRejected(t *testing.T) {
	recursive := eventDrivenRecipe + `use_recipe:
  - recipe: event-driven
    as: nested
`
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": ordersInstantiation},
		map[string]string{"event-driven.yaml": recursive})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("use_recipe inside a recipe must be rejected")
	}
	if !strings.Contains(err.Error(), "use_recipe inside a recipe is not supported") {
		t.Fatalf("the error must name the recursion rejection, got: %v", err)
	}
}

func TestRecipeProblems_DeadRoleWarnsWithoutFailing(t *testing.T) {
	withDeadRole := strings.Replace(eventDrivenRecipe, "roles:\n", "roles:\n  - name: audit\n", 1)
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": ordersInstantiation},
		map[string]string{"event-driven.yaml": withDeadRole})
	recipes, loadProblems, err := LoadRecipesDir(dir)
	if err != nil || len(loadProblems) > 0 {
		t.Fatalf("load = (%v, %v)", loadProblems, err)
	}
	problems, warnings := RecipeProblems(recipes)
	if len(problems) != 0 {
		t.Fatalf("a dead role must not fail validation: %v", problems)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `role "audit" is referenced by no rule (dead role)`) {
		t.Fatalf("warnings = %v, want the dead-role warning", warnings)
	}
	if _, err := LoadRepoFile(dir); err != nil {
		t.Fatalf("a dead role must not fail the load: %v", err)
	}
}

func TestLoadRepoFile_ExemptionNamingUnknownRuleFails(t *testing.T) {
	stray := ordersInstantiation + `    exempt:
      - rule: events-are-consumed
        witness: "whatever"
        owner: "dana"
        because: "typo in the rule name"
        since: "2026-08-11"
`
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": stray},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("an exemption naming a rule the recipe lacks must be an error")
	}
	if !strings.Contains(err.Error(), `names rule "events-are-consumed", which recipe event-driven does not declare`) {
		t.Fatalf("the error must name the missing rule and the recipe's rules, got: %v", err)
	}
}

func TestLoadRepoFile_InstanceExemptionMissingOwnerFailsAtItsInstance(t *testing.T) {
	unsigned := ordersInstantiation + `    exempt:
      - rule: events-consumed
        witness: "SomeEvent has no inbound calls edge from orders-events/handlers"
        because: "unsigned"
        since: "2026-08-11"
`
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": unsigned},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("an unsigned instance exemption must be an error")
	}
	if !strings.Contains(err.Error(), "use_recipe orders-events (recipe event-driven) rule events-consumed") ||
		!strings.Contains(err.Error(), "missing owner") {
		t.Fatalf("the error must cite the instance and the unsigned exemption, got: %v", err)
	}
}

func TestLoadRepoFile_InstanceModeOverridesPerRuleDefaults(t *testing.T) {
	perRuleAdvisory := strings.Replace(eventDrivenRecipe,
		"    pattern: \"*Event\"\n", "    pattern: \"*Event\"\n    mode: advisory\n", 1)
	overridden := strings.Replace(billingInstantiation, "    as: billing-events\n", "    as: billing-events\n    mode: advisory\n", 1)
	dir := writeRecipeRepo(t, "",
		map[string]string{"billing.yaml": overridden, "orders.yaml": ordersInstantiation},
		map[string]string{"event-driven.yaml": perRuleAdvisory})
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]any{}
	for _, f := range CompileFacts(d) {
		if f.Props["intent_kind"] == "rule" {
			modes[f.Props["rule"].(string)] = f.Props["mode"]
		}
	}
	if modes["orders-events/events-consumed"] != "ratchet" {
		t.Fatalf("an instance with no mode keeps the recipe default, got %v", modes["orders-events/events-consumed"])
	}
	if modes["orders-events/events-are-named"] != "advisory" {
		t.Fatalf("a per-rule recipe mode is the default, got %v", modes["orders-events/events-are-named"])
	}
	for _, id := range []string{"billing-events/events-consumed", "billing-events/only-bus-calls-handlers", "billing-events/events-are-named"} {
		if modes[id] != "advisory" {
			t.Fatalf("an instance-wide mode overrides every rule: %s = %v", id, modes[id])
		}
	}
}

func TestLoadRepoFile_InvalidInstanceModeFails(t *testing.T) {
	invalid := strings.Replace(ordersInstantiation, "    as: orders-events\n", "    as: orders-events\n    mode: hard\n", 1)
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": invalid},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("an unknown instance mode must be an error")
	}
	if !strings.Contains(err.Error(), `mode "hard" is not an enforcement or guidance mode`) {
		t.Fatalf("the error must name the allowed modes, got: %v", err)
	}
}

func TestLoadRepoFile_InvalidBindingSelectorCitesInstanceAndRole(t *testing.T) {
	badGlob := strings.Replace(ordersInstantiation, `["app/events/orders/**"]`, `["app/events/*.rb"]`, 1)
	dir := writeRecipeRepo(t, "",
		map[string]string{"orders.yaml": badGlob},
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("an out-of-dialect binding pattern must be an error")
	}
	if !strings.Contains(err.Error(), "use_recipe orders-events (recipe event-driven) role events") {
		t.Fatalf("the error must cite the instance and role that declared the pattern, got: %v", err)
	}
}

func TestLoadRepoFile_InlineUseRecipeRejected(t *testing.T) {
	dir := writeRecipeRepo(t, ordersInstantiation,
		nil,
		map[string]string{"event-driven.yaml": eventDrivenRecipe})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("use_recipe in the inline declaration must be rejected")
	}
	if !strings.Contains(err.Error(), "use_recipe is not inline vocabulary") {
		t.Fatalf("the error must route the author to the constraints directory, got: %v", err)
	}
}

func TestLoadRepoFile_RecipeExpansionIsDeterministic(t *testing.T) {
	files := map[string]string{"billing.yaml": billingInstantiation, "orders.yaml": ordersInstantiation}
	recipes := map[string]string{"event-driven.yaml": eventDrivenRecipe}
	first, err := LoadRepoFile(writeRecipeRepo(t, "", files, recipes))
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadRepoFile(writeRecipeRepo(t, "", files, recipes))
	if err != nil {
		t.Fatal(err)
	}
	firstCompiled, err := json.Marshal(CompileFacts(first))
	if err != nil {
		t.Fatal(err)
	}
	secondCompiled, err := json.Marshal(CompileFacts(second))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCompiled, secondCompiled) {
		t.Fatalf("two expansions of one repo differ:\nfirst:  %s\nsecond: %s", firstCompiled, secondCompiled)
	}
}

// Every form the table knows survives expansion with its roles bound to the
// instance, and every counterpart role with them. The fixtures are the
// enumeration's own, so a form added to the schema without a fixture fails
// there first and a form whose roles the expansion forgets fails here: the
// five graph-law forms expanded to the bare role name, which no declared
// component carries, until this walk read the table instead of a hand list.
func TestExpandRules_EveryFormRebindsItsRoles(t *testing.T) {
	for _, form := range RuleForms {
		fixture, ok := formFixtures[form.Key]
		if !ok {
			t.Fatalf("form %q has no fixture", form.Key)
		}
		rule := fixture.build(func(role string) string { return role })
		rule.ID = form.Key
		var roles []RecipeRole
		for _, role := range fixture.roles {
			roles = append(roles, RecipeRole{Name: role})
		}
		rec := Recipe{Name: "r", Roles: roles, Rules: []ConstraintRule{rule}}
		inst := RecipeInstantiation{Recipe: "r", As: "inst", Bind: map[string]RecipeBinding{}}
		for _, role := range fixture.roles {
			inst.Bind[role] = RecipeBinding{Match: []string{"app/" + role + "/**"}}
		}
		expanded := expandRules(rec, inst, nil, "enola/constraints/x.yaml")
		if len(expanded) != 1 {
			t.Fatalf("%s: expanded %d rules, want 1", form.Key, len(expanded))
		}
		got := ruleRoleReferences(expanded[0])
		want := ruleRoleReferences(rule)
		if len(got) != len(want) {
			t.Fatalf("%s: expansion changed the role count: %v -> %v", form.Key, want, got)
		}
		for _, ref := range got {
			if !strings.HasPrefix(ref, "inst/") && ref != secondStep {
				t.Fatalf("%s: role %q left unbound after expansion (%v)", form.Key, ref, got)
			}
		}
		if form.Subject(expanded[0]) != "inst/"+fixture.roles[0] {
			t.Fatalf("%s: subject = %q, want inst/%s", form.Key, form.Subject(expanded[0]), fixture.roles[0])
		}
	}
}

func TestExpandBindings_RoleDefaultsCarryHandlesPublicGovernedBy(t *testing.T) {
	rec := Recipe{Name: "r", Roles: []RecipeRole{
		{Name: "mutating-actions", Kind: "symbol", Match: []string{"app/controllers/**"}, Handles: []string{"POST", "DELETE"}},
		{Name: "api", Match: []string{"app/controllers/api/**"}, Public: []string{"app/controllers/api/public/**"}, GovernedBy: "wiki/api/**"},
	}}
	inst := RecipeInstantiation{Recipe: "r", As: "inst", Bind: map[string]RecipeBinding{
		"api": {Handles: []string{"PATCH"}},
	}}
	components := expandBindings(rec, inst, "enola/constraints/x.yaml")
	byRole := map[string]ConstraintComponent{}
	for _, c := range components {
		byRole[c.Role] = c
	}
	actions := byRole["mutating-actions"]
	if len(actions.Handles) != 2 || actions.Handles[0] != "POST" {
		t.Fatalf("a defaulted role inherits handles: %+v", actions)
	}
	api := byRole["api"]
	if len(api.Handles) != 1 || api.Handles[0] != "PATCH" {
		t.Fatalf("a binding's handles override the role's: %+v", api)
	}
	if len(api.Public) != 1 || api.GovernedBy != "wiki/api/**" {
		t.Fatalf("public and governed_by inherit from the role: %+v", api)
	}
}
