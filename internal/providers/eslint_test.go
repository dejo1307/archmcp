package providers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The ESLint reference provider, driven end to end through the seam from a
// results file: one lint fact per reported message, parse errors without a
// rule id dropped, files made repository-relative, severity named, output
// sorted, and the seam's provenance stamped on.
func TestESLintProvider_TurnsResultsIntoLintFacts(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "examples", "providers", "js", "eslint", "enola_eslint_provider.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	results := filepath.Join(repo, "tmp", "eslint-results.json")
	if err := os.MkdirAll(filepath.Dir(results), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(results, []byte(`[
  {"filePath":"`+filepath.Join(repo, "ember_app/app/components/a.gjs")+`","messages":[
    {"ruleId":"tt/no-mutating-args","severity":1,"message":"Do not write through args","line":12,"column":5},
    {"ruleId":"tt/no-mutating-args","severity":1,"message":"Do not write through args","line":40,"column":3},
    {"ruleId":null,"severity":2,"message":"Parsing error","line":1,"column":1}]},
  {"filePath":"`+filepath.Join(repo, "ember_app/app/b.ts")+`","messages":[
    {"ruleId":"tt/ember-declare-type","severity":2,"message":"Declare the type","line":3,"column":1}]}
]`), 0o644); err != nil {
		t.Fatal(err)
	}

	ff, records := Run(context.Background(), []Provider{{Name: "eslint", Command: []string{"node", script}, ExpectedVersion: "0.1.0"}}, repo, nil)
	if len(records) != 1 || records[0].Skipped {
		t.Fatalf("census = %+v", records)
	}
	if len(ff) != 3 {
		t.Fatalf("facts = %+v, want 3 (the rule-less parse error dropped)", ff)
	}
	if ff[2].Name != "eslint: tt/no-mutating-args ember_app/app/components/a.gjs #2" || ff[2].Line != 40 {
		t.Fatalf("a second finding of the same rule in the same file takes the next ordinal, never its line: %+v", ff[2])
	}
	first := ff[0]
	if first.Kind != facts.KindLint || first.File != "ember_app/app/b.ts" || first.Line != 3 ||
		first.Props["lint_rule"] != "tt/ember-declare-type" || first.Props["lint_severity"] != "error" ||
		first.Props[PropResolutionLevel] != LevelToolReported || first.Props[PropProvider] != "eslint" {
		t.Fatalf("first fact = %+v", first)
	}
	if ff[1].Name != "eslint: tt/no-mutating-args ember_app/app/components/a.gjs" || ff[1].Props["lint_severity"] != "warn" || ff[1].Line != 12 {
		t.Fatalf("second fact = %+v", ff[1])
	}
}

// No results anywhere: the provider emits nothing and exits 0, which the seam
// records as a provider that ran and contributed nothing, never an error.
func TestESLintProvider_NoResultsIsNothingNotAnError(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	script, _ := filepath.Abs(filepath.Join("..", "..", "examples", "providers", "js", "eslint", "enola_eslint_provider.mjs"))
	ff, records := Run(context.Background(), []Provider{{Name: "eslint", Command: []string{"node", script}, ExpectedVersion: "0.1.0"}}, t.TempDir(), nil)
	if len(ff) != 0 || len(records) != 1 || records[0].Skipped || records[0].FactCount != 0 {
		t.Fatalf("facts = %d, census = %+v", len(ff), records)
	}
}
