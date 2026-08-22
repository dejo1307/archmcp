package intent

import (
	"os"
	"sort"
	"strings"
	"testing"
)

const fullSurface = `
Enola.architecture "estate" do
  part :handlers, files: "internal/handlers/**"
  part :storage, files: "internal/storage/**"
  part :getters, files: "app/**", kind: :symbol, where: { symbol_kind: "getter" }
  part :events, files: "app/events/**", service: "backend"

  law "a handler never unwraps a promise by hand" do
    id "handlers-unwrap-nothing"
    handlers.must_not_call "*.unwrapPromise", "*.getPromiseState"
    why "the renderer re-runs the getter on every read"
    mode :advisory
  end

  law "a promise getter memoises" do
    getters.must_carry prop: "decorators", value: "cached"
    when_calling "*.unwrapPromise", via: :calls
    why "an unmemoised getter recomputes on every read and re-renders on the new value"
  end

  law "every event is consumed" do
    events.must_be_reached_by handlers
    direction :inbound
    why "an event nobody handles is a message written into the dark"
  end

  law "reach for the helper that exists" do
    handlers.advises "prefer the shared formatter over a new one"
    exemplar "internal/handlers/format.go"
    mode :advisory
  end

  law "storage stays behind the handlers" do
    storage.stays_inside except: handlers
    why "a caller that reaches past the handler cannot be rate limited"
    exempt "internal/storage/legacy.go", because: "the importer predates the boundary", owner: "platform", since: "2026-08-21"
  end
end
`

// The surface says everything the vocabulary says: an explicit id, a literal
// far end, the require form's antecedent, an explicit direction, guidance with
// its prior art, and an exemption with its reason.
func TestRubySurface_ExpressesTheWholeVocabulary(t *testing.T) {
	file, problems := ParseRubySurface([]byte(fullSurface), "enola/constraints/architecture.rb")
	if len(problems) != 0 {
		t.Fatalf("the declaration must compile: %s", strings.Join(problems, "; "))
	}
	rules := map[string]ConstraintRule{}
	for _, r := range file.Rules {
		rules[r.ID] = r
	}
	if got := rules["handlers-unwrap-nothing"]; got.Forbid != "handlers" || len(got.ToName) != 2 || got.ToName[0] != "*.unwrapPromise" {
		t.Fatalf("explicit id and literal far ends: %+v", got)
	}
	if got := rules["a-promise-getter-memoises"]; got.Require != "getters" ||
		got.MustPropContain == nil || got.MustPropContain.Value != "cached" ||
		len(got.WhenEdgeTo) != 1 || got.Via != "calls" {
		t.Fatalf("require with its antecedent: %+v", got)
	}
	if got := rules["every-event-is-consumed"]; got.RequireEdge != "events" || got.Direction != "inbound" {
		t.Fatalf("explicit direction: %+v", got)
	}
	if got := rules["reach-for-the-helper-that-exists"]; got.Guide != "handlers" || len(got.Exemplars) != 1 {
		t.Fatalf("guidance with prior art: %+v", got)
	}
	got := rules["storage-stays-behind-the-handlers"]
	if got.Private != "storage" || len(got.Except) != 1 || len(got.Exempt) != 1 || got.Exempt[0].Owner != "platform" {
		t.Fatalf("visibility with an exemption: %+v", got)
	}
	if err := (&Declaration{Components: file.Components, Rules: file.Rules}).Validate(); err != nil {
		t.Fatalf("the compiled declaration must validate: %v", err)
	}
}

// A recipe is the bundle somebody else wrote, instantiated against this
// repository's own parts, which is how a team adopts a convention set without
// authoring it.
func TestRubySurface_InstantiatesARecipe(t *testing.T) {
	src := `
Enola.architecture "estate" do
  use_recipe :ember_conventions, as: :app, mode: :advisory do
    bind :components, files: "app/components/**"
    bind :fetchers, files: "app/services/**", kind: :symbol, where: { symbol_kind: "class" }
  end
end
`
	file, problems := ParseRubySurface([]byte(src), "enola/constraints/recipes.rb")
	if len(problems) != 0 {
		t.Fatalf("a recipe instantiation must compile: %s", strings.Join(problems, "; "))
	}
	if len(file.UseRecipe) != 1 {
		t.Fatalf("recipes = %+v", file.UseRecipe)
	}
	use := file.UseRecipe[0]
	if use.Recipe != "ember_conventions" || use.As != "app" || use.Mode != "advisory" {
		t.Fatalf("instantiation header: %+v", use)
	}
	if len(use.Bind) != 2 || use.Bind["components"].Match[0] != "app/components/**" ||
		use.Bind["fetchers"].Where["symbol_kind"] != "class" {
		t.Fatalf("bindings: %+v", use.Bind)
	}
}

// The premise this surface rests on is that everything the declaration
// vocabulary says can be said as a sentence. This walks the rule schema itself
// and fails when a key has no way in, so a key added later without a sentence
// breaks the build rather than quietly having no surface.
func TestRubySurface_EveryDeclarationKeyHasAWayIn(t *testing.T) {
	source, err := os.ReadFile("rubysurface.go")
	if err != nil {
		t.Fatal(err)
	}
	surface := string(source)
	// Keys the surface fills from the sentence itself rather than from a
	// written key: the subject and its form, the reason, and the file each
	// declaration came from.
	derived := map[string]bool{"because": true, "source": true}
	for _, form := range RuleForms {
		derived[form.Key] = true
	}
	for _, role := range CounterpartRoles {
		derived[role.Key] = true
	}
	missing := []string{}
	for _, key := range []string{"id", "to_name", "when_prop_contains", "when_edge_to", "when_via",
		"direction", "exemplars", "message", "mode", "via", "pattern", "surface", "method",
		"max_members", "must_prop_contain", "exempt"} {
		if derived[key] {
			continue
		}
		field := strings.ReplaceAll(strings.Title(strings.ReplaceAll(key, "_", " ")), " ", "")
		if !strings.Contains(surface, "rule."+field) && !strings.Contains(surface, `"`+key+`"`) {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("these declaration keys cannot be said in the surface: %v", missing)
	}
}
