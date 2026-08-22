package intent

import (
	"strings"
	"testing"
)

// The three forms added on 2026-08-22 each read as a sentence: parts that
// must not cycle with each other, a part whose modules never reach their
// includers, and a protocol satisfied by one of several methods.
func TestRubySurface_NewVocabularySentences(t *testing.T) {
	src := `
Enola.architecture "storefront" do
  rails
  part :services, files: "app/services/**"

  law "jobs, models and mailers never depend on each other in a circle" do
    jobs.must_not_cycle_with :models, :mailers
    why "parts that reach each other in a circle cannot be taken apart"
  end

  law "a concern never reaches the class that includes it" do
    concerns.must_not_reach_includers
    why "a mixin that knows its includer is half a class in hiding"
  end

  law "a service answers to one of two doors" do
    services.must_define_one_of :call, :run
    why "either door is fine; none is a class nobody can invoke"
  end
end
`
	file, problems := ParseRubySurface([]byte(src), "enola/constraints/vocabulary.rb")
	if len(problems) != 0 {
		t.Fatalf("the sentences must compile: %s", strings.Join(problems, "; "))
	}
	if len(file.Rules) != 3 {
		t.Fatalf("rules = %+v", file.Rules)
	}
	cycles, independent, anyOf := file.Rules[0], file.Rules[1], file.Rules[2]
	if cycles.ForbidCycles != "jobs" || strings.Join(cycles.Among, ",") != "models,mailers" {
		t.Fatalf("cycles: %+v", cycles)
	}
	if independent.Independent != "concerns" {
		t.Fatalf("independent: %+v", independent)
	}
	if anyOf.RequireDefines != "services" || strings.Join(anyOf.AnyOf, ",") != "call,run" || anyOf.Method != "" {
		t.Fatalf("any_of: %+v", anyOf)
	}
	d := &Declaration{Components: file.Components, Rules: file.Rules}
	if problems := d.Problems(); len(problems) != 0 {
		t.Fatalf("the compiled declaration must validate: %v", problems)
	}
}

func TestVocabulary_AnyOfAndMethodAreExclusive(t *testing.T) {
	d := &Declaration{
		Components: []ConstraintComponent{{Name: "services", Match: []string{"app/services/**"}}},
		Rules:      []ConstraintRule{{ID: "r", RequireDefines: "services", Method: "call", AnyOf: []string{"call", "run"}, Because: "x"}},
	}
	problems := d.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "declare exactly one") {
		t.Fatalf("problems = %v", problems)
	}
	d.Rules[0] = ConstraintRule{ID: "r", ForbidCycles: "services", Because: "x"}
	problems = d.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "needs among") {
		t.Fatalf("problems = %v", problems)
	}
}
