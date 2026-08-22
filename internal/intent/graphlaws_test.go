package intent

import (
	"strings"
	"testing"
)

// A recipe role that declares its selector binds with nothing but the
// instantiation naming the recipe; a binding that gives its own keys
// overrides the defaults key by key.
func TestRecipeRoleDefaults_BindWithoutSelectors(t *testing.T) {
	rec := Recipe{
		Name: "front-end",
		Roles: []RecipeRole{
			{Name: "actions", Kind: "symbol", Where: map[string]any{"decorators": "action"}},
			{Name: "components", Match: []string{"app/components/**"}},
			{Name: "fixtures", Match: []string{"tests/mirage/**"}},
		},
		Rules: []ConstraintRule{
			{ID: "no-action-decorator", ForbidFact: "actions", Because: "x"},
			{ID: "tests-carry-no-fixtures", Forbid: "components", To: "fixtures", Via: "calls", Because: "y"},
		},
	}
	inst := RecipeInstantiation{Recipe: "front-end", As: "app", Bind: map[string]RecipeBinding{
		"components": {Match: []string{"ember_app/app/components/**"}},
	}}
	components := expandBindings(rec, inst, "enola/constraints/front-end.yaml")
	byName := map[string]ConstraintComponent{}
	for _, c := range components {
		byName[c.Name] = c
	}
	if len(components) != 3 {
		t.Fatalf("every defaulted role binds, got %d components", len(components))
	}
	if byName["app/actions"].Kind != "symbol" || byName["app/actions"].Where["decorators"] != "action" {
		t.Fatalf("the role's selector is the binding's, got %+v", byName["app/actions"])
	}
	if got := byName["app/components"].Match; len(got) != 1 || got[0] != "ember_app/app/components/**" {
		t.Fatalf("a binding's own paths override the default, got %v", got)
	}
	if got := byName["app/fixtures"].Match; len(got) != 1 || got[0] != "tests/mirage/**" {
		t.Fatalf("an unbound role takes its default paths, got %v", got)
	}
	if required := RequiredRoles(rec); len(required) != 0 {
		t.Fatalf("a defaulted role is never required of the binding, got %v", required)
	}
}

func TestRubySurface_GraphLawSentences(t *testing.T) {
	src := `
Enola.architecture "shop" do
  part :billing, files: "app/billing/**"
  part :mutating_actions, files: "app/controllers/**", handles: [:post, :put, :patch, :delete]
  part :policies, files: "app/policies/**"
  part :api, files: "config/**", kind: :route
  part :tables, files: "app/models/**", kind: :storage
  part :old_way, governed_by: "wiki/shop/adrs/*.md status:superseded"

  law "billing keeps to its own tables" do
    billing.storage_must_stay_home
    since "2026-08-01"
    why "a part that writes another part's table owns a bug it cannot see"
  end

  law "a mutating action is authorized" do
    mutating_actions.must_reach :policies
    why "every write passes a policy"
  end

  law "frames keep their query budget" do
    billing.must_keep_budget metric: :queries, max: 20
    why "a frame past twenty queries is a page that will not scale"
  end

  law "every route has a consumer" do
    api.must_have_consumer
    why "a route nobody calls is a surface nobody maintains"
  end

  law "one owner per table" do
    tables.must_be_unique_across by: :table
    why "two writers to one table disagree in the end"
  end

  law "the old way is documented" do
    old_way.must_be_governed
    why "code under a retired decision needs a page that says so"
  end
end
`
	file, problems := ParseRubySurface([]byte(src), "enola/constraints/laws.rb")
	if len(problems) != 0 {
		t.Fatalf("the sentences must compile: %s", strings.Join(problems, "; "))
	}
	if len(file.Rules) != 6 {
		t.Fatalf("rules = %+v", file.Rules)
	}
	byID := map[string]ConstraintRule{}
	for _, r := range file.Rules {
		byID[r.ID] = r
	}
	home := file.Rules[0]
	if home.StorageStaysHome != "billing" || home.Since != "2026-08-01" {
		t.Fatalf("storage law: %+v", home)
	}
	if budget := file.Rules[2]; budget.CapRuntime != "billing" || budget.Metric != "queries" || budget.Max != 20 {
		t.Fatalf("budget law: %+v", budget)
	}
	if file.Rules[3].RequireConsumer != "api" || file.Rules[4].UniqueAcross != "tables" || file.Rules[4].By != "table" || file.Rules[5].RequireGoverned != "old-way" {
		t.Fatalf("rules: %+v", file.Rules[3:])
	}
	var actions ConstraintComponent
	for _, c := range file.Components {
		if c.Name == "mutating-actions" {
			actions = c
		}
	}
	if strings.Join(actions.Handles, ",") != "post,put,patch,delete" {
		t.Fatalf("handles: %+v", actions)
	}
	d := &Declaration{Components: file.Components, Rules: file.Rules}
	if problems := d.Problems(); len(problems) != 0 {
		t.Fatalf("the compiled declaration must validate: %v", problems)
	}
}

func TestGraphLaws_ValidationNamesTheMissingKey(t *testing.T) {
	d := &Declaration{
		Components: []ConstraintComponent{{Name: "api", Match: []string{"config/**"}, Kind: "route"}},
		Rules:      []ConstraintRule{{ID: "r", CapRuntime: "api", Because: "x"}},
	}
	problems := d.Problems()
	if len(problems) != 2 || !strings.Contains(problems[0], "metric") || !strings.Contains(problems[1], "positive max") {
		t.Fatalf("problems = %v", problems)
	}
	d.Rules[0] = ConstraintRule{ID: "r", UniqueAcross: "api", Because: "x"}
	if problems := d.Problems(); len(problems) != 1 || !strings.Contains(problems[0], "needs by") {
		t.Fatalf("problems = %v", problems)
	}
	d.Rules[0] = ConstraintRule{ID: "r", Cap: "api", MaxMembers: 3, Since: "yesterday", Because: "x"}
	if problems := d.Problems(); len(problems) != 1 || !strings.Contains(problems[0], "calendar date") {
		t.Fatalf("problems = %v", problems)
	}
	d.Rules[0] = ConstraintRule{ID: "r", ForbidFact: "api", Growth: 2, Because: "x"}
	if problems := d.Problems(); len(problems) != 1 || !strings.Contains(problems[0], "growth belongs to cap") {
		t.Fatalf("problems = %v", problems)
	}
	d.Components[0].Handles = []string{"FETCH"}
	d.Rules[0] = ConstraintRule{ID: "r", ForbidFact: "api", Because: "x"}
	if problems := d.Problems(); len(problems) != 2 || !strings.Contains(problems[0], "not an HTTP method") || !strings.Contains(problems[1], "kind must be symbol") {
		t.Fatalf("problems = %v", problems)
	}
}
