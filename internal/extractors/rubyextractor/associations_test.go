package rubyextractor

import (
	"os"
	"path/filepath"
	"testing"
)

func associationFixture(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"app/models/company.rb": `class Company < ApplicationRecord
  belongs_to :manager, class_name: "User"
  has_many :candidates
  has_many :approvable_steps
  has_many :step_verdicts, through: :approvable_steps, source: :verdicts
  has_one :stranger, through: :approvable_steps
  belongs_to :subject, polymorphic: true
  has_many :ghosts
end
`,
		"app/models/user.rb":      "class User < ApplicationRecord\nend\n",
		"app/models/candidate.rb": "class Candidate < ApplicationRecord\nend\n",
		"app/models/approvable_step.rb": `class ApprovableStep < ApplicationRecord
  has_many :verdicts, class_name: "ApprovableStepVerdict"
end
`,
		"app/models/approvable_step_verdict.rb":         "class ApprovableStepVerdict < ApplicationRecord\nend\n",
		"app/models/webhook_connected_event.rb":         "class WebhookConnectedEvent < ApplicationRecord\n  belongs_to :company\nend\n",
		"app/models/webhook_connected_event/company.rb": "class WebhookConnectedEvent::Company < WebhookConnectedEvent\nend\n",
		"app/models/concerns/trackable.rb":              "module Trackable\n  has_many :events\nend\n",
	}
	var list []string
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		list = append(list, rel)
	}
	return dir, list
}

func TestAssociationsResolveEachDeterminationPath(t *testing.T) {
	dir, files := associationFixture(t)
	got, _ := extractAssociations(dir, files)

	targets := map[string]string{}
	sources := map[string]string{}
	for _, fact := range got {
		targets[fact.Name] = fact.Props["target"].(string)
		sources[fact.Name] = fact.Props["target_source"].(string)
	}

	for _, c := range []struct{ name, target, via string }{
		{"Company#manager", "User", targetDeclared},
		{"Company#candidates", "Candidate", targetDerived},
		{"Company#step_verdicts", "ApprovableStepVerdict", targetThrough},
		// Ruby resolves a constant in the enclosing namespace first, and an STI
		// subclass of a model is itself a model.
		{"WebhookConnectedEvent#company", "WebhookConnectedEvent::Company", targetDerived},
	} {
		if targets[c.name] != c.target {
			t.Errorf("%s: target = %q, want %q", c.name, targets[c.name], c.target)
		}
		if sources[c.name] != c.via {
			t.Errorf("%s: target_source = %q, want %q", c.name, sources[c.name], c.via)
		}
	}
}

// TestAssociationsEmitNoEdgeWithoutATarget is the decision this whole extractor
// rests on: every consumer treats an edge as something you can follow.
func TestAssociationsEmitNoEdgeWithoutATarget(t *testing.T) {
	dir, files := associationFixture(t)
	got, unresolved := extractAssociations(dir, files)

	for _, fact := range got {
		switch fact.Name {
		case "Company#subject", "Company#stranger", "Company#ghosts", "Trackable#events":
			t.Errorf("%s must not be an edge: %v", fact.Name, fact.Props)
		}
	}
	for reason, want := range map[string]int{
		"polymorphic_association": 1,
		"through_association":     1,
		"unknown_model":           1,
	} {
		if unresolved[reason] != want {
			t.Errorf("%s counted %d, want %d — a refusal nothing counts is the failure this mechanism exists for",
				reason, unresolved[reason], want)
		}
	}
}

// TestThroughSourceNamesAnAssociationNotAClass pins the bug measurement found:
// camelizing `source: :verdicts` yields Verdict, which no model answers to. The
// answer lives on the intermediate model's own declaration.
func TestThroughSourceNamesAnAssociationNotAClass(t *testing.T) {
	dir, files := associationFixture(t)
	got, _ := extractAssociations(dir, files)
	for _, fact := range got {
		if fact.Name != "Company#step_verdicts" {
			continue
		}
		if target := fact.Props["target"].(string); target == "Verdict" {
			t.Fatalf("source: was camelized into a class name that does not exist")
		}
	}
}
