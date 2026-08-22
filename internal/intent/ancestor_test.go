package intent

import (
	"strings"
	"testing"
)

// `ancestor:` is a selector in its own right, so a component carrying only it
// selects, and the name has to read as a constant path so a typo is refused
// at declaration time rather than selecting nothing at snapshot time.
func TestAncestorKey_DeclaredInYAML(t *testing.T) {
	d := &Declaration{Components: []ConstraintComponent{
		{Name: "records", Ancestor: "ApplicationRecord"},
		{Name: "components", Match: []string{"app/components/**"}, Ancestor: "ViewComponent::Base"},
	}}
	if problems := d.Problems(); len(problems) != 0 {
		t.Fatalf("an ancestor component must validate: %v", problems)
	}
	compiled := CompileFacts(d)
	var found bool
	for _, f := range compiled {
		if f.PropString("component") == "records" && f.PropString("ancestor") == "ApplicationRecord" {
			found = true
		}
	}
	if !found {
		t.Fatal("the compiled intent fact must carry the ancestor so the evaluator reads it")
	}
	bad := &Declaration{Components: []ConstraintComponent{{Name: "records", Ancestor: "application record"}}}
	problems := bad.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "must be a constant path") {
		t.Fatalf("problems = %v", problems)
	}
}
