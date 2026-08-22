package engine_test

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/extractors/rubyextractor"
	"github.com/enola-labs/enola/internal/extractors/tsextractor"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// The matrix: every rule form that walks an edge, every role a component can
// occupy in it, over an edge kind that rides MEMBER facts (calls) and one that
// rides DEPENDENCY facts (imports). Coverage is driven from intent.RuleForms
// and intent.CounterpartRoles, so a form added to the schema without a case
// here fails this test rather than slipping past the screen.
//
// Every fixture is built by the real extractors. The estate the previous rounds
// verified against put calls edges directly on CLASS facts, which the Ruby
// extractor never does — a class's calls ride its Owner#method and Owner.method
// facts — so a matrix built on hand-written literals is accidentally true and
// passes while the shipped behaviour is broken. Nothing here writes a fact.

const (
	// A model with a class method, so a calls target can name a fact a member
	// owns rather than the member itself.
	fixtureJob = `class Job < ApplicationRecord
  def self.build
    new
  end
end
`
	// The concept: selected by what it carries, not by where it sits. Its calls
	// ride its methods, which is the whole of the problem.
	fixtureTimeoutError = `class TimeoutError < StandardError
  def self.notify_all
    true
  end

  def notify
    Job.build
  end
end
`
	fixtureCard = `class Card
  def render
    TimeoutError.notify_all
    Job.build
  end
end
`
	fixtureMailer = `class Mailer
  def self.deliver
    true
  end
end
`
)

// conceptRepo is the fixture the matrix runs against: four Ruby files under
// twenty facts, one concept selected by superclass.
func conceptRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "shop")
	writeFile(t, filepath.Join(repo, "app", "models", "job.rb"), fixtureJob)
	writeFile(t, filepath.Join(repo, "app", "errors", "timeout_error.rb"), fixtureTimeoutError)
	writeFile(t, filepath.Join(repo, "app", "views", "card.rb"), fixtureCard)
	writeFile(t, filepath.Join(repo, "app", "mailers", "mailer.rb"), fixtureMailer)
	return repo
}

// orphanRepo drops the concept's only caller, so an inbound demand over it is
// TOTALLY violated — the case a reach test asking the breach's own question
// silences, and the fifth reproduction.
func orphanRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "shop")
	writeFile(t, filepath.Join(repo, "app", "models", "job.rb"), fixtureJob)
	writeFile(t, filepath.Join(repo, "app", "errors", "timeout_error.rb"), fixtureTimeoutError)
	writeFile(t, filepath.Join(repo, "app", "mailers", "mailer.rb"), fixtureMailer)
	return repo
}

type declarationFile struct {
	Components []intent.ConstraintComponent `yaml:"components"`
	Rules      []intent.ConstraintRule      `yaml:"rules"`
}

func concept(owns string, match []string, namePattern string) intent.ConstraintComponent {
	return intent.ConstraintComponent{
		Name:        "exceptions",
		Where:       map[string]any{"superclass": "StandardError"},
		Owns:        owns,
		Match:       match,
		NamePattern: namePattern,
	}
}

// owningConcept is the declaration that makes a class's methods' edges the
// class's: the one every calls-side case is written against.
func owningConcept() intent.ConstraintComponent { return concept(intent.OwnsMethods, nil, "") }

func matrixComponents(c intent.ConstraintComponent) []intent.ConstraintComponent {
	return []intent.ConstraintComponent{
		c,
		{Name: "models", Match: []string{"app/models/**"}, Kind: "symbol"},
		{Name: "views", Match: []string{"app/views/**"}, Kind: "symbol"},
		{Name: "mailers", Match: []string{"app/mailers/**"}, Kind: "symbol"},
	}
}

func matrixRule(r intent.ConstraintRule) intent.ConstraintRule {
	r.ID = "matrix-rule"
	r.Because = "the matrix states every rule's reason, as every rule must"
	return r
}

// snapshotInsights runs one declaration against one fixture through the real
// extractors and returns the constraints explainer's findings. A declaration
// the screen refuses fails the snapshot, which is what screening at declaration
// time buys: the rule never compiles into a fact, so no explainer sees it.
func snapshotInsights(t *testing.T, repo string, d declarationFile) ([]facts.Insight, error) {
	t.Helper()
	encoded, err := yaml.Marshal(declared(d))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, intent.RepoFileName), string(encoded))
	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(rubyextractor.New())
	eng.RegisterExtractor(tsextractor.New())
	eng.SetPersistCache(false)
	snap, err := eng.GenerateSnapshot(context.Background(), repo, false)
	if err != nil {
		return nil, err
	}
	store := facts.NewStore()
	store.Add(snap.Facts...)
	insights, err := constraints.New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	return insights, nil
}

// declared renders the declaration as the YAML a repository writes: only the
// fields the case set. A whole-struct marshal emits every key, and an empty
// where — which the validator refuses as a predicate that narrows nothing — is
// not what any of these cases declare.
func declared(d declarationFile) map[string]any {
	components := make([]map[string]any, 0, len(d.Components))
	for _, c := range d.Components {
		entry := map[string]any{"name": c.Name}
		put(entry, "service", c.Service)
		put(entry, "kind", c.Kind)
		put(entry, "name_pattern", c.NamePattern)
		put(entry, "owns", c.Owns)
		if len(c.Match) > 0 {
			entry["match"] = c.Match
		}
		if len(c.Where) > 0 {
			entry["where"] = c.Where
		}
		components = append(components, entry)
	}
	rules := make([]map[string]any, 0, len(d.Rules))
	for _, r := range d.Rules {
		entry := map[string]any{"id": r.ID, "because": r.Because}
		put(entry, "forbid", r.Forbid)
		put(entry, "forbid_reach", r.ForbidReach)
		put(entry, "to", r.To)
		put(entry, "allow", r.Allow)
		put(entry, "protect", r.Protect)
		put(entry, "private", r.Private)
		put(entry, "require_edge", r.RequireEdge)
		put(entry, "direction", r.Direction)
		put(entry, "protocol", r.Protocol)
		put(entry, "via", r.Via)
		putAll(entry, "only", r.Only)
		putAll(entry, "owners", r.Owners)
		putAll(entry, "except", r.Except)
		putAll(entry, "steps", r.Steps)
		if len(r.Owns) > 0 {
			owns := make([]map[string]any, 0, len(r.Owns))
			for _, o := range r.Owns {
				owns = append(owns, map[string]any{"component": o.Component, "owns": o.Owns})
			}
			entry["owns"] = owns
		}
		rules = append(rules, entry)
	}
	return map[string]any{"components": components, "rules": rules}
}

func put(entry map[string]any, key, value string) {
	if value != "" {
		entry[key] = value
	}
}

func putAll(entry map[string]any, key string, values []string) {
	if len(values) > 0 {
		entry[key] = values
	}
}

func breachWitnesses(insights []facts.Insight) []string {
	var out []string
	for _, insight := range insights {
		if witness := constraints.WitnessFromTitle(insight.Title); witness != "" {
			out = append(out, witness)
		}
	}
	sort.Strings(out)
	return out
}

func unverdictableTitles(insights []facts.Insight) []string {
	var out []string
	for _, insight := range insights {
		if strings.Contains(insight.Title, "cannot verdict") {
			out = append(out, insight.Title)
		}
	}
	sort.Strings(out)
	return out
}

// outcome is what one case expects, and exactly one of its three arms is the
// claim: a concept in an edge role either verdicts with a stated basis, or is
// refused before it compiles, or is refused at verdict time. Never silently.
type outcome struct {
	refusedAtDeclaration string
	unverdictable        bool
	breaches             []string
}

type matrixCase struct {
	name    string
	form    string
	role    string
	via     string
	orphan  bool
	concept intent.ConstraintComponent
	rule    intent.ConstraintRule
	want    outcome
}

func matrixCases() []matrixCase {
	return []matrixCase{
		// calls — the edge kind that rides member facts. The concept owns what
		// its members' methods, which is what makes any of these resolvable.
		{name: "forbid subject sources through what it owns", form: "forbid", role: "forbid", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Forbid: "exceptions", To: "models", Via: "calls"}),
			want:    outcome{breaches: []string{"TimeoutError#notify -> Job via calls", "TimeoutError#notify -> Job.build via calls"}}},
		{name: "forbid to is named exactly and by what a member owns", form: "forbid", role: "to", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Forbid: "views", To: "exceptions", Via: "calls"}),
			want:    outcome{breaches: []string{"Card#render -> TimeoutError via calls", "Card#render -> TimeoutError.notify_all via calls"}}},
		{name: "forbid_reach subject walks from what it owns", form: "forbid_reach", role: "forbid_reach", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{ForbidReach: "exceptions", To: "models", Via: "calls"}),
			want:    outcome{breaches: []string{"TimeoutError#notify reaches Job", "TimeoutError#notify reaches Job.build"}}},
		{name: "forbid_reach to is reached through what a member owns", form: "forbid_reach", role: "to", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{ForbidReach: "views", To: "exceptions", Via: "calls"}),
			want:    outcome{breaches: []string{"Card#render reaches TimeoutError", "Card#render reaches TimeoutError.notify_all"}}},
		{name: "allow subject sources through what it owns", form: "allow", role: "allow", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Allow: "exceptions", Only: []string{"views"}, Via: "calls"}),
			want:    outcome{breaches: []string{"TimeoutError#notify -> Job via calls", "TimeoutError#notify -> Job.build via calls"}}},
		// Reproduction 3: a landing that IS a method of the only allowed concept
		// class. It drew a false 1.0 breach because the class's own name was the
		// only thing only: could absorb.
		{name: "only absorbs a landing on what the allowed concept owns", form: "allow", role: "only", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Allow: "views", Only: []string{"exceptions"}, Via: "calls"}),
			want:    outcome{breaches: []string{"Card#render -> Job via calls", "Card#render -> Job.build via calls"}}},
		{name: "protect subject is reached through what a member owns", form: "protect", role: "protect", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Protect: "exceptions", Owners: []string{"models"}, Via: "calls"}),
			want:    outcome{breaches: []string{"Card#render -> TimeoutError via calls", "Card#render -> TimeoutError.notify_all via calls"}}},
		// Reproduction 1: the owners: role over calls. ownedBy resolved a calls
		// source, which in Ruby is always a method fact, against a component whose
		// members are class facts — so every edge read as unowned.
		{name: "owners owns the edges its members' methods make", form: "protect", role: "owners", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Protect: "models", Owners: []string{"exceptions"}, Via: "calls"}),
			want:    outcome{breaches: []string{"Card#render -> Job via calls", "Card#render -> Job.build via calls"}}},
		// private walks every rule-via kind, imports among them, and its subject
		// is a target-side role: an imports target names a path, so it reaches a
		// component only through the measured file grounding joins to match globs.
		{name: "private subject over imports needs a file join it does not have", form: "private", role: "private", via: "imports",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Private: "exceptions"}),
			want:    outcome{refusedAtDeclaration: "cannot sit in the private role of a rule walking imports"}},
		// Reproduction 2: except: over a rule that walks imports. A concept cannot
		// SOURCE an imports edge at all — they ride dependency facts carrying none
		// of the props a predicate tests — so an except: that excepts nothing made
		// every reach a trespass.
		{name: "except cannot source the imports edges private walks", form: "private", role: "except", via: "imports",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Private: "models", Except: []string{"exceptions"}}),
			want:    outcome{refusedAtDeclaration: "cannot sit in the except role of a rule walking imports"}},
		{name: "require_edge subject is satisfied through what it owns", form: "require_edge", role: "require_edge", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{RequireEdge: "exceptions", Direction: "inbound", Via: "calls"}),
			want:    outcome{}},
		// Reproduction 5: the same rule against a total violation. The previous
		// round reported ZERO breaches, because the blindness test and the breach
		// condition were the same predicate — dead-code enforcement inverted
		// exactly where it matters.
		{name: "require_edge reports a total violation rather than going quiet", form: "require_edge", role: "require_edge", via: "calls", orphan: true,
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{RequireEdge: "exceptions", Direction: "inbound", Via: "calls"}),
			want:    outcome{breaches: []string{"TimeoutError has no inbound calls edge"}}},
		{name: "require_edge to is a source scope reaching through what it owns", form: "require_edge", role: "to", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{RequireEdge: "models", Direction: "inbound", Via: "calls", To: "exceptions"}),
			want:    outcome{}},
		{name: "protocol subject makes its step edges through what it owns", form: "protocol", role: "protocol", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Protocol: "exceptions", Steps: []string{"views", "models"}, Via: "calls"}),
			want:    outcome{breaches: []string{"TimeoutError calls models without views"}}},
		{name: "steps is reached through what a member owns", form: "protocol", role: "steps", via: "calls",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Protocol: "views", Steps: []string{"mailers", "exceptions"}, Via: "calls"}),
			want:    outcome{breaches: []string{"Card#render calls exceptions without mailers"}}},

		// imports — the edge kind that rides dependency facts. No ownership in
		// this vocabulary reaches a file's dependency facts, so a concept can
		// never source one, and it can only be NAMED by one through grounding.
		{name: "forbid subject cannot source an imports edge", form: "forbid", role: "forbid", via: "imports",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Forbid: "exceptions", To: "models", Via: "imports"}),
			want:    outcome{refusedAtDeclaration: "cannot sit in the forbid role of a rule walking imports"}},
		{name: "imports to without match globs cannot be grounded onto", form: "forbid", role: "to", via: "imports",
			concept: owningConcept(),
			rule:    matrixRule(intent.ConstraintRule{Forbid: "views", To: "exceptions", Via: "imports"}),
			want:    outcome{refusedAtDeclaration: "cannot sit in the to role of a rule walking imports"}},
		{name: "imports to with match globs is refused at verdict when nothing grounds", form: "forbid", role: "to", via: "imports",
			concept: concept(intent.OwnsMethods, []string{"app/errors/**"}, ""),
			rule:    matrixRule(intent.ConstraintRule{Forbid: "views", To: "exceptions", Via: "imports"}),
			want:    outcome{unverdictable: true}},
		// Reproduction 4: only: over imports with name_pattern + where + match.
		// grounding.inComponent returns false outright when namePattern is set,
		// and the blindness check never consulted namePattern.
		{name: "only over imports with a name_pattern grounds onto nothing", form: "allow", role: "only", via: "imports",
			concept: concept(intent.OwnsMethods, []string{"app/errors/**"}, "TimeoutError"),
			rule:    matrixRule(intent.ConstraintRule{Allow: "views", Only: []string{"exceptions"}, Via: "imports"}),
			want:    outcome{refusedAtDeclaration: "cannot sit in the only role of a rule walking imports"}},
	}
}

func TestConceptEdgeMatrix(t *testing.T) {
	covered := map[string]bool{}
	vias := map[string]bool{}
	for _, c := range matrixCases() {
		covered[c.form+"/"+c.role] = true
		vias[c.via] = true
		t.Run(c.form+"/"+c.role+"/"+c.via+"/"+c.name, func(t *testing.T) {
			repo := conceptRepo(t)
			if c.orphan {
				repo = orphanRepo(t)
			}
			insights, err := snapshotInsights(t, repo, declarationFile{
				Components: matrixComponents(c.concept),
				Rules:      []intent.ConstraintRule{c.rule},
			})
			if c.want.refusedAtDeclaration != "" {
				if err == nil {
					t.Fatalf("the declaration must be refused before it compiles; insights = %v", breachWitnesses(insights))
				}
				if !strings.Contains(err.Error(), c.want.refusedAtDeclaration) {
					t.Fatalf("refusal = %v, want it to name %q", err, c.want.refusedAtDeclaration)
				}
				return
			}
			if err != nil {
				t.Fatalf("the declaration must compile: %v", err)
			}
			refused := unverdictableTitles(insights)
			if c.want.unverdictable {
				if len(refused) == 0 {
					t.Fatalf("insights = %+v, want a refusal: the role resolves no %s edge", insights, c.via)
				}
				if got := breachWitnesses(insights); len(got) != 0 {
					t.Fatalf("breaches = %v, want none: a rule that cannot verdict must emit no verdict", got)
				}
				return
			}
			if len(refused) != 0 {
				t.Fatalf("refusals = %v, want none: the role resolves through the side it uses", refused)
			}
			got := breachWitnesses(insights)
			if strings.Join(got, "|") != strings.Join(c.want.breaches, "|") {
				t.Fatalf("breaches = %v, want %v", got, c.want.breaches)
			}
		})
	}

	for _, form := range intent.RuleForms {
		if form.WalksEdges && !covered[form.Key+"/"+form.Key] {
			t.Errorf("rule form %q walks edges and its subject role is in no matrix case", form.Key)
		}
	}
	for _, role := range intent.CounterpartRoles {
		found := false
		for pair := range covered {
			if strings.HasSuffix(pair, "/"+role.Key) {
				found = true
			}
		}
		if !found {
			t.Errorf("counterpart role %q is in no matrix case — every role a rule fills with a component must be exercised", role.Key)
		}
	}
	for _, via := range []string{"calls", "imports"} {
		if !vias[via] {
			t.Errorf("the matrix never runs over %s — one edge kind riding member facts and one riding dependency facts is the claim", via)
		}
	}
}

// The verdict states the basis it reached each end on, and an owned end says
// so rather than claiming an exactness the resolution did not have.
func TestConceptEdgeMatrix_AnOwnedEndIsNotCalledExact(t *testing.T) {
	insights, err := snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(owningConcept()),
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			Forbid: "exceptions", To: "models", Via: "calls"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if constraints.WitnessFromTitle(insight.Title) == "" {
			continue
		}
		if strings.Contains(insight.Description, "both memberships are exact") {
			t.Errorf("the source is a method of the member, not a member: %q", insight.Description)
		}
		if !strings.Contains(insight.Description, "the source is a method of a member, which the declaration says is the member's") {
			t.Errorf("the verdict must state the owned basis, got: %q", insight.Description)
		}
	}
}

// Without the declaration the rule does not quietly do the wrong thing: it does
// not compile at all. This is the same rule as the first matrix case, minus the
// one field, and it is what every previous round shipped instead.
func TestConceptEdgeMatrix_WithoutOwnershipTheRuleDoesNotCompile(t *testing.T) {
	_, err := snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(concept("", nil, "")),
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			Forbid: "exceptions", To: "models", Via: "calls"})},
	})
	if err == nil {
		t.Fatal("a concept in an edge role with no declared ownership must be refused")
	}
	if !strings.Contains(err.Error(), "nothing declares what it owns") {
		t.Fatalf("refusal = %v, want it to name the missing declaration", err)
	}
}

// An explicit `nothing` is a different statement from an absent one, and the
// vocabulary has to keep them apart: the component says its members' own edges
// are all that count, the rule compiles, and the class sources no calls — so
// the reach check refuses at verdict time rather than reading the silence as a
// clean pass.
func TestConceptEdgeMatrix_OwningNothingCompilesAndIsRefusedOnReach(t *testing.T) {
	insights, err := snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(concept(intent.OwnsNothing, nil, "")),
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			Forbid: "exceptions", To: "models", Via: "calls"})},
	})
	if err != nil {
		t.Fatalf("an explicit ownership compiles: %v", err)
	}
	if got := unverdictableTitles(insights); len(got) != 1 {
		t.Fatalf("refusals = %v, want one: the class sources no calls edge and the rule must not read that as a clean pass", got)
	}
	if got := breachWitnesses(insights); len(got) != 0 {
		t.Fatalf("breaches = %v, want none", got)
	}
}

// The precedence, pinned in both directions on a running snapshot. A rule's
// override wins over the component's declaration whichever way it points, and
// the verdicts move with it.
func TestConceptEdgeMatrix_RuleOverridesComponentBothWays(t *testing.T) {
	permissive := matrixRule(intent.ConstraintRule{
		Forbid: "exceptions", To: "models", Via: "calls",
		Owns: []intent.ComponentOwnership{{Component: "exceptions", Owns: intent.OwnsMethods}}})
	insights, err := snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(concept(intent.OwnsNothing, nil, "")),
		Rules:      []intent.ConstraintRule{permissive},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := breachWitnesses(insights); len(got) != 2 {
		t.Fatalf("breaches = %v, want the two the owned methods' edges make: the rule's override widens what the component declared", got)
	}

	strict := matrixRule(intent.ConstraintRule{
		Forbid: "exceptions", To: "models", Via: "calls",
		Owns: []intent.ComponentOwnership{{Component: "exceptions", Owns: intent.OwnsNothing}}})
	insights, err = snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(concept(intent.OwnsMethods, nil, "")),
		Rules:      []intent.ConstraintRule{strict},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := breachWitnesses(insights); len(got) != 0 {
		t.Fatalf("breaches = %v, want none: the rule's override narrows what the component declared", got)
	}
	if got := unverdictableTitles(insights); len(got) != 1 {
		t.Fatalf("refusals = %v, want one: narrowed to its members' own edges the concept reaches no calls edge", got)
	}
}

// A concept CAN be named by an imports edge where the target grounds onto a
// file it measured a member in — the arm the Ruby matrix cannot exercise,
// because Ruby's imports edges join directories rather than files.
func TestConceptEdgeMatrix_ImportsTargetGroundsOntoAConceptsMemberFile(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "web")
	writeFile(t, filepath.Join(repo, "tsconfig.json"), "{\"compilerOptions\":{}}\n")
	writeFile(t, filepath.Join(repo, "src", "errors", "app_error.ts"), "export class AppError extends Error {}\n")
	writeFile(t, filepath.Join(repo, "src", "errors", "timeout.ts"),
		"import { AppError } from './app_error';\n\nexport class TimeoutError extends AppError {\n  notify() {\n    return true;\n  }\n}\n")
	writeFile(t, filepath.Join(repo, "src", "views", "card.ts"),
		"import { TimeoutError } from '../errors/timeout';\n\nexport function render() {\n  return new TimeoutError();\n}\n")
	insights, err := snapshotInsights(t, repo, declarationFile{
		Components: []intent.ConstraintComponent{
			{Name: "exceptions", Where: map[string]any{"superclass": "AppError"}, Owns: intent.OwnsMethods, Match: []string{"src/errors/**"}},
			{Name: "views", Match: []string{"src/views/**"}},
		},
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			Forbid: "views", To: "exceptions", Via: "imports"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := unverdictableTitles(insights); len(got) != 0 {
		t.Fatalf("refusals = %v, want none: the imports target grounds onto the file the concept measured its member in", got)
	}
	got := breachWitnesses(insights)
	if len(got) == 0 {
		t.Fatalf("insights = %+v, want the grounded breach", insights)
	}
	for _, insight := range insights {
		if constraints.WitnessFromTitle(insight.Title) == "" {
			continue
		}
		if !strings.Contains(insight.Description, "grounds on the measured file it names") {
			t.Errorf("the verdict must state that its target grounded, got: %q", insight.Description)
		}
	}
}
