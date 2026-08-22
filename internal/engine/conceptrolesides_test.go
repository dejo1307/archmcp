package engine_test

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/intent"
)

// The side a role resolves against is the single most load-bearing structural
// claim in this vocabulary, and until this file nothing measured it: flipping
// owners: from source to target left TestConceptEdgeMatrix and every companion
// green, because the estate those cases run on answers both readings the same
// way. Only two tests failed, and both built their store from fact literals —
// the shape that made five earlier rounds accidentally pass.
//
// The guard here is extractor-driven like the matrix, and it is per role rather
// than per form: every entry of intent.RuleForms that walks an edge and every
// entry of intent.CounterpartRoles gets a case, including the two roles whose
// side is a function of the rule rather than a constant.
//
// It reads the side through the one screen that words the two readings
// differently. A concept sitting in a role of a rule walking imports is refused
// whichever end it resolves against, but for opposite reasons — as the SOURCE
// because every imports edge rides a dependency fact no ownership reaches, and
// as the TARGET because an imports target names a path that needs match globs
// to ground onto. So the refusal names the side, and a flipped side cannot
// produce the sentence the shipped side earns.

const (
	sourceSideRefusal = "every imports edge rides a dependency fact"
	targetSideRefusal = "an imports target names a path"
)

// roleSideCase puts the concept in exactly ONE role, so the refusal it earns is
// attributable to that role alone. Every other component in the rule is
// path-selected, which the screen does not question.
type roleSideCase struct {
	name string
	role string
	side string
	rule intent.ConstraintRule
}

func roleSideCases() []roleSideCase {
	return []roleSideCase{
		{name: "forbid subject sources the edge it forbids", role: "forbid", side: intent.SideSource,
			rule: matrixRule(intent.ConstraintRule{Forbid: "exceptions", To: "models", Via: "imports"})},
		{name: "forbid_reach subject sources the walk", role: "forbid_reach", side: intent.SideSource,
			rule: matrixRule(intent.ConstraintRule{ForbidReach: "exceptions", To: "models", Via: "imports"})},
		{name: "allow subject sources the edge it allows", role: "allow", side: intent.SideSource,
			rule: matrixRule(intent.ConstraintRule{Allow: "exceptions", Only: []string{"models"}, Via: "imports"})},
		{name: "protect subject is what the edge lands on", role: "protect", side: intent.SideTarget,
			rule: matrixRule(intent.ConstraintRule{Protect: "exceptions", Owners: []string{"models"}, Via: "imports"})},
		{name: "private subject is what the edge lands on", role: "private", side: intent.SideTarget,
			rule: matrixRule(intent.ConstraintRule{Private: "exceptions"})},
		// The two roles whose side is a function of the rule. An inbound demand
		// is about edges landing ON the member, an outbound one about edges the
		// member makes, and a constant Side would answer for only one of them.
		{name: "require_edge subject is the target of an inbound demand", role: "require_edge", side: intent.SideTarget,
			rule: matrixRule(intent.ConstraintRule{RequireEdge: "exceptions", Direction: "inbound", Via: "imports"})},
		{name: "require_edge subject is the source of an outbound demand", role: "require_edge", side: intent.SideSource,
			rule: matrixRule(intent.ConstraintRule{RequireEdge: "exceptions", Direction: "outbound", Via: "imports"})},
		{name: "protocol subject sources its step edges", role: "protocol", side: intent.SideSource,
			rule: matrixRule(intent.ConstraintRule{Protocol: "exceptions", Steps: []string{"models", "views"}, Via: "imports"})},
		{name: "to is the far end of a forbidden edge", role: "to", side: intent.SideTarget,
			rule: matrixRule(intent.ConstraintRule{Forbid: "views", To: "exceptions", Via: "imports"})},
		{name: "to is the source an inbound demand must come from", role: "to", side: intent.SideSource,
			rule: matrixRule(intent.ConstraintRule{RequireEdge: "models", Direction: "inbound", Via: "imports", To: "exceptions"})},
		{name: "owners names the sources allowed to reach a protected component", role: "owners", side: intent.SideSource,
			rule: matrixRule(intent.ConstraintRule{Protect: "models", Owners: []string{"exceptions"}, Via: "imports"})},
		{name: "only names the landings an allowed component may reach", role: "only", side: intent.SideTarget,
			rule: matrixRule(intent.ConstraintRule{Allow: "views", Only: []string{"exceptions"}, Via: "imports"})},
		{name: "except names a source excused from a private component", role: "except", side: intent.SideSource,
			rule: matrixRule(intent.ConstraintRule{Private: "models", Except: []string{"exceptions"}})},
		{name: "steps names the stages an edge must have reached", role: "steps", side: intent.SideTarget,
			rule: matrixRule(intent.ConstraintRule{Protocol: "views", Steps: []string{"models", "exceptions"}, Via: "imports"})},
	}
}

func otherSide(side string) string {
	if side == intent.SideSource {
		return intent.SideTarget
	}
	return intent.SideSource
}

func refusalFor(side string) string {
	if side == intent.SideSource {
		return sourceSideRefusal
	}
	return targetSideRefusal
}

// refuseSide runs one case through the real extractors and returns the reason
// clause the screen refused it with, which is the side it read.
func refuseSide(t *testing.T, c roleSideCase) string {
	t.Helper()
	_, err := snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(owningConcept()),
		Rules:      []intent.ConstraintRule{c.rule},
	})
	if err == nil {
		t.Fatalf("%s: the declaration must be refused — a concept in the %s role of a rule walking imports resolves against nothing on either end", c.name, c.role)
	}
	message := err.Error()
	if !strings.Contains(message, "cannot sit in the "+c.role+" role of a rule walking imports") {
		t.Fatalf("%s: refusal = %v, want it to name the %s role", c.name, err, c.role)
	}
	switch {
	case strings.Contains(message, sourceSideRefusal):
		return intent.SideSource
	case strings.Contains(message, targetSideRefusal):
		return intent.SideTarget
	}
	t.Fatalf("%s: refusal = %v, want it to word one of the two readings", c.name, err)
	return ""
}

// flipRoleSide inverts one role's side in the schema tables both the screen and
// the explainer read, and returns the restore. It wraps the shipped function
// rather than replacing it with a constant, so a side that is a function of the
// rule is inverted for every rule rather than flattened to one answer.
func flipRoleSide(t *testing.T, role string) func() {
	t.Helper()
	for i := range intent.RuleForms {
		if intent.RuleForms[i].Key != role || !intent.RuleForms[i].WalksEdges {
			continue
		}
		shipped := intent.RuleForms[i].Side
		intent.RuleForms[i].Side = func(r intent.ConstraintRule) string { return otherSide(shipped(r)) }
		return func() { intent.RuleForms[i].Side = shipped }
	}
	for i := range intent.CounterpartRoles {
		if intent.CounterpartRoles[i].Key != role {
			continue
		}
		shipped := intent.CounterpartRoles[i].Side
		intent.CounterpartRoles[i].Side = func(r intent.ConstraintRule) string { return otherSide(shipped(r)) }
		return func() { intent.CounterpartRoles[i].Side = shipped }
	}
	t.Fatalf("role %q is in neither schema table, so nothing states which end it resolves against", role)
	return func() {}
}

// Every role resolves against the end the schema says it does, measured through
// the real extractors.
func TestConceptEdgeMatrix_EveryRoleResolvesAgainstItsDeclaredSide(t *testing.T) {
	for _, c := range roleSideCases() {
		t.Run(c.role+"/"+c.side+"/"+c.name, func(t *testing.T) {
			if got := refuseSide(t, c); got != c.side {
				t.Fatalf("the screen read the %s role as %s, want %s — the refusal it earns is %q", c.role, got, c.side, refusalFor(c.side))
			}
		})
	}
}

// And the guard is not vacuous: flipping each role's side in turn makes the
// case above fail, one role at a time. Without this the side table could be
// rewritten wholesale and every extractor-driven test would stay green, which
// is exactly what shipped.
func TestConceptEdgeMatrix_FlippingARoleSideIsCaught(t *testing.T) {
	for _, c := range roleSideCases() {
		t.Run(c.role+"/"+c.side+"/"+c.name, func(t *testing.T) {
			restore := flipRoleSide(t, c.role)
			defer restore()
			got := refuseSide(t, c)
			if got == c.side {
				t.Fatalf("the %s role still read as %s with its side flipped — nothing in this case measures the side", c.role, got)
			}
			if got != otherSide(c.side) {
				t.Fatalf("the flipped %s role read as %s, want %s", c.role, got, otherSide(c.side))
			}
		})
	}
}

// The two tables are covered entirely, so a role added to either without a case
// here fails rather than shipping unguarded.
func TestConceptEdgeMatrix_EveryRoleHasASideCase(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range roleSideCases() {
		covered[c.role] = true
	}
	for _, form := range intent.RuleForms {
		if form.WalksEdges && !covered[form.Key] {
			t.Errorf("rule form %q walks edges and no case measures which end its subject resolves against", form.Key)
		}
	}
	for _, role := range intent.CounterpartRoles {
		if !covered[role.Key] {
			t.Errorf("counterpart role %q has no case measuring which end it resolves against", role.Key)
		}
	}
}
