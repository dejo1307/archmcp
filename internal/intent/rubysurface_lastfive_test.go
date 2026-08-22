package intent

import (
	"strings"
	"testing"
)

// The five spellings added on 2026-08-22: a paired naming law, a part with a
// public surface by path, and a literal far end narrowed to receiver-less
// calls.
func TestRubySurface_LastFiveShapes(t *testing.T) {
	src := `
Enola.architecture "storefront" do
  rails
  part :chat, files: "app/chat/**"
  part :billing, files: "app/billing/**", public: "app/billing/public/**"

  law "every with_ scope has a without_ twin" do
    chat.names_must_match "with_*", requires: "without_*"
    why "a reader expects the pair"
  end

  law "billing is reached through its public files" do
    billing.stays_inside
    why "the rest is implementation"
  end

  law "a bare params read belongs to the controller" do
    models.must_not_call "params", receiver: :none
    why "without a receiver it is the request's"
  end
end
`
	file, problems := ParseRubySurface([]byte(src), "enola/constraints/five.rb")
	if len(problems) != 0 {
		t.Fatalf("the sentences must compile: %s", strings.Join(problems, "; "))
	}
	var billing ConstraintComponent
	for _, c := range file.Components {
		if c.Name == "billing" {
			billing = c
		}
	}
	if len(billing.Public) != 1 || billing.Public[0] != "app/billing/public/**" {
		t.Fatalf("public: %+v", billing)
	}
	if len(file.Rules) != 3 {
		t.Fatalf("rules = %+v", file.Rules)
	}
	pairs, private, bare := file.Rules[0], file.Rules[1], file.Rules[2]
	if pairs.RequireName != "chat" || pairs.Pattern != "with_*" || pairs.Requires != "without_*" {
		t.Fatalf("pairs: %+v", pairs)
	}
	if private.Private != "billing" {
		t.Fatalf("private: %+v", private)
	}
	if bare.Forbid != "models" || strings.Join(bare.ToName, ",") != "params" || bare.Receiver != "none" {
		t.Fatalf("bare: %+v", bare)
	}
	d := &Declaration{Components: file.Components, Rules: file.Rules}
	if problems := d.Problems(); len(problems) != 0 {
		t.Fatalf("the compiled declaration must validate: %v", problems)
	}
}

func TestLastFiveShapes_Validation(t *testing.T) {
	models := ConstraintComponent{Name: "models", Match: []string{"app/models/**"}}
	for name, tc := range map[string]struct {
		rule   ConstraintRule
		wantIn string
	}{
		"requires needs one star each side": {ConstraintRule{ID: "r", RequireName: "models", Pattern: "with_*", Requires: "without", Because: "x"}, "one * in pattern and one * in requires"},
		"requires belongs to require_name":  {ConstraintRule{ID: "r", ForbidName: "models", Pattern: "get_*", Requires: "set_*", Because: "x"}, "belongs to the require_name form"},
		"receiver needs a literal far end":  {ConstraintRule{ID: "r", Forbid: "models", To: "models", Via: "calls", Receiver: "none", Because: "x"}, "belongs to the forbid form with a to_name literal"},
		"receiver is none or any":           {ConstraintRule{ID: "r", Forbid: "models", ToName: []string{"params"}, Via: "calls", Receiver: "self", Because: "x"}, "none or any"},
	} {
		t.Run(name, func(t *testing.T) {
			problems := (&Declaration{Components: []ConstraintComponent{models}, Rules: []ConstraintRule{tc.rule}}).Problems()
			if len(problems) == 0 || !strings.Contains(strings.Join(problems, "; "), tc.wantIn) {
				t.Fatalf("problems = %v, want %q", problems, tc.wantIn)
			}
		})
	}
	bad := &Declaration{Components: []ConstraintComponent{{Name: "billing", Match: []string{"app/billing/**"}, Public: []string{"app/billing/{a,b}/**"}}}}
	if problems := bad.Problems(); len(problems) != 1 || !strings.Contains(problems[0], "public[0]") {
		t.Fatalf("public globs are bounded: %v", problems)
	}
}
