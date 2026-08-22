package intent

import (
	"strings"
	"testing"
)

// A part selected by ancestry is said as a sentence like every other selector,
// on a part and on a recipe binding, and it compiles to the declaration key
// the evaluator reads.
func TestRubySurface_PartTakesAncestor(t *testing.T) {
	src := `
Enola.architecture "storefront" do
  part :records, ancestor: "ApplicationRecord"
  part :components, files: "app/components/**", ancestor: "ViewComponent::Base"
  use_recipe "rails-conventions", as: :rails do
    bind :models, ancestor: "ApplicationRecord"
  end
end
`
	file, problems := ParseRubySurface([]byte(src), "enola/constraints/ancestry.rb")
	if len(problems) != 0 {
		t.Fatalf("an ancestor part must compile: %s", strings.Join(problems, "; "))
	}
	byName := map[string]ConstraintComponent{}
	for _, c := range file.Components {
		byName[c.Name] = c
	}
	if byName["records"].Ancestor != "ApplicationRecord" || len(byName["records"].Match) != 0 {
		t.Fatalf("records: %+v", byName["records"])
	}
	if byName["components"].Ancestor != "ViewComponent::Base" || byName["components"].Match[0] != "app/components/**" {
		t.Fatalf("components: %+v", byName["components"])
	}
	if len(file.UseRecipe) != 1 || file.UseRecipe[0].Bind["models"].Ancestor != "ApplicationRecord" {
		t.Fatalf("bind: %+v", file.UseRecipe)
	}
	if problems := (&Declaration{Components: file.Components}).Problems(); len(problems) != 0 {
		t.Fatalf("the compiled declaration must validate: %v", problems)
	}
}

// The name has to read as a constant path; anything else is refused at
// declaration time rather than selecting nothing at snapshot time.
func TestRubySurface_AncestorMustBeAConstantPath(t *testing.T) {
	d := &Declaration{Components: []ConstraintComponent{{Name: "records", Ancestor: "application record"}}}
	problems := d.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "must be a constant path") {
		t.Fatalf("problems = %v", problems)
	}
}
