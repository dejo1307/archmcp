package intent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const railsSurface = `
Enola.architecture "storefront" do
  rails
  part :maintenance, files: "app/tasks/**"
  part :service_objects, files: ["app/services/**"], kind: :symbol, where: { symbol_kind: "class" }

  law "background jobs never invoke controller code" do
    jobs.must_not_call controllers
    why "rendering from a job goes through ApplicationController.renderer"
    seen_in "2,552 of 2,557 call edges"
  end

  law "every maintenance task lives in the Maintenance namespace" do
    maintenance.names_must_match "Maintenance::*"
    why "the gem resolves task constants from it"
  end

  law "a service object has exactly one door" do
    service_objects.must_define :call
    why "callers never reach a second public method"
    mode :advisory
  end

  law "no get_ prefixes on a model's public surface" do
    models.names_must_not_match "get_*", surface: :exported
    why "a reader is a noun"
  end
end
`

func rulesByID(file ConstraintsFile) map[string]ConstraintRule {
	out := map[string]ConstraintRule{}
	for _, r := range file.Rules {
		out[r.ID] = r
	}
	return out
}

// The Rails example reads as English and compiles to the declaration the YAML
// loader produces, with the vocabulary a `rails` line gives for free.
func TestRubySurface_CompilesTheRailsExample(t *testing.T) {
	file, problems := ParseRubySurface([]byte(railsSurface), "enola/constraints/architecture.rb")
	if len(problems) != 0 {
		t.Fatalf("the example must compile cleanly: %v", problems)
	}
	parts := map[string]ConstraintComponent{}
	for _, c := range file.Components {
		parts[c.Name] = c
	}
	if got := parts["jobs"]; len(got.Match) != 1 || got.Match[0] != "app/jobs/**" {
		t.Fatalf("rails must declare jobs so an edge law can name them: %+v", got)
	}
	if got := parts["maintenance"]; len(got.Match) != 1 || got.Match[0] != "app/tasks/**" {
		t.Fatalf("a part's files: %+v", got)
	}
	if got := parts["service-objects"]; got.Where["symbol_kind"] != "class" || got.Kind != "symbol" {
		t.Fatalf("a part's where: %+v", got)
	}

	rules := rulesByID(file)
	edge := rules["background-jobs-never-invoke-controller-code"]
	if edge.Forbid != "jobs" || edge.To != "controllers" || edge.Via != "calls" {
		t.Fatalf("must_not_call compiles to forbid/to/calls: %+v", edge)
	}
	if !strings.Contains(edge.Because, "ApplicationController.renderer") || !strings.Contains(edge.Because, "2,552") {
		t.Fatalf("the reason and its measurement both survive: %q", edge.Because)
	}
	if got := rules["every-maintenance-task-lives-in-the-maintenance-namespace"]; got.RequireName != "maintenance" || got.Pattern != "Maintenance::*" {
		t.Fatalf("names_must_match compiles to require_name: %+v", got)
	}
	if got := rules["a-service-object-has-exactly-one-door"]; got.RequireDefines != "service-objects" || got.Method != "call" || got.Mode != "advisory" {
		t.Fatalf("must_define and mode: %+v", got)
	}
	if got := rules["no-get-prefixes-on-a-model-s-public-surface"]; got.ForbidName != "models" || got.Pattern != "get_*" || got.Surface != "exported" {
		t.Fatalf("names_must_not_match and surface: %+v", got)
	}
	if err := (&Declaration{Components: file.Components, Rules: file.Rules}).Validate(); err != nil {
		t.Fatalf("a compiled declaration must satisfy the same validator YAML does: %v", err)
	}
}

// The premise this work rests on: every rule form the vocabulary declares is
// reachable from a sentence. A form with no sentence is a gap in the surface,
// and this test is what makes that gap visible the day a form is added.
func TestRubySurface_EveryRuleFormHasASentence(t *testing.T) {
	covered := map[string]bool{}
	for _, edge := range edgeVerbs {
		covered[edge.form] = true
	}
	for _, form := range memberVerbs {
		covered[form] = true
	}
	var missing []string
	for _, form := range RuleForms {
		if !covered[form.Key] {
			missing = append(missing, form.Key)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("these forms cannot be said in the surface: %v", missing)
	}
}

// A line that is not a declaration, a part that selects nothing, a law with no
// statement and a subject that was never declared are all refused with the
// line they sit on, because a declaration read by a person must fail where the
// person can see it.
func TestRubySurface_RefusesWhatItCannotMean(t *testing.T) {
	src := `
Enola.architecture "x" do
  part :ghosts
  configure_something :else
  law "jobs behave" do
    jobs.must_not_call controllers
  end
  law "empty" do
    why "nothing stated"
  end
end
`
	_, problems := ParseRubySurface([]byte(src), "enola/constraints/architecture.rb")
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		"part \"ghosts\" selects nothing",
		"is not a declaration",
		"\"jobs\" is not a part",
		"says nothing",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing problem %q in:\n%s", want, joined)
		}
	}
	for _, p := range problems {
		if !strings.HasPrefix(p, "enola/constraints/architecture.rb:") {
			t.Errorf("every problem cites its file and line: %q", p)
		}
	}
}

// The loader reads both spellings from one directory, stamps each with its
// declaring file, and merges them in sorted order, so a repository can hold a
// YAML law and a Ruby law at once.
func TestLoadConstraintsDir_ReadsBothSpellings(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "enola", "constraints")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yamlLaw := "components:\n  - name: legacy\n    match: [\"app/legacy/**\"]\nrules:\n  - id: legacy-is-frozen\n    forbid_fact: legacy\n    because: \"app/legacy is frozen\"\n"
	if err := os.WriteFile(filepath.Join(dir, "a-legacy.yaml"), []byte(yamlLaw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b-architecture.rb"), []byte(railsSurface), 0o644); err != nil {
		t.Fatal(err)
	}
	files, problems, err := LoadConstraintsDir(repo)
	if err != nil || len(problems) != 0 {
		t.Fatalf("both spellings must load: %v %v", err, problems)
	}
	if len(files) != 2 || files[0].Path != "enola/constraints/a-legacy.yaml" || files[1].Path != "enola/constraints/b-architecture.rb" {
		t.Fatalf("sorted merge order: %+v", files)
	}
	for _, rule := range files[1].Rules {
		if rule.SourceFile != "enola/constraints/b-architecture.rb" {
			t.Fatalf("a Ruby law is stamped with its declaring file: %+v", rule)
		}
	}
	merged := MergeConstraintsFiles(nil, files)
	if err := merged.Validate(); err != nil {
		t.Fatalf("the merged declaration must validate: %v", err)
	}
}
