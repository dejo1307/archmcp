package intent

import (
	"fmt"
	"sort"
	"strings"
)

// The form vocabulary, written once. Every surface that has to answer "which
// forms are there" reads these two tables instead of repeating the list, so a
// form added to ConstraintRule without an entry here is missing from the arity
// message, from the undeclared-component check and from the edge-role screen at
// the same time — which the enumeration test in ruleforms_test.go turns into a
// failure rather than a silent gap.

// The two ends of a measured edge. A role resolves a component against ONE of
// them and never both: the subject of forbid is the source of the edge it
// forbids, while the subject of protect is what the edge lands ON. Asking a
// role the question belonging to the other end is how an edge pointing the
// wrong way was read as reach — deleting an unrelated inbound edge flipped an
// owners: rule from a false breach to a correct refusal, which is a verdict
// depending on a fact nobody's rule mentioned.
const (
	SideSource = "source"
	SideTarget = "target"
)

// RuleForm is one entry of the closed form vocabulary: the declaration key that
// selects the form, the accessor that reads the component that key names, and
// whether the form resolves its components against a MEASURED EDGE.
type RuleForm struct {
	Key     string
	Subject func(ConstraintRule) string
	// WalksEdges marks the forms whose verdict is a claim about edges: they
	// resolve their components as the SOURCE or the TARGET of a measured
	// relation. The rest read a member's own props and never leave the fact
	// that made it a member.
	WalksEdges bool
	// Side names the end of the walked edge this form's SUBJECT resolves
	// against. It takes the rule because require_edge's subject changes ends
	// with its direction: an inbound demand is about edges landing ON the
	// member, an outbound one about edges the member makes.
	Side func(ConstraintRule) string
	// CensusMeasured marks the existential forms, which decide from the
	// extraction census whether their subject's absence of an edge is
	// measurable. It excuses the subject from the verdict-time reach screen on
	// ONE end only — the end an edge lands on, where an empty resolution is the
	// breach itself and screening it would silence the total violation the form
	// exists to catch. The census is keyed on (repo, file extension) and cannot
	// say which facts in a file carry an edge, so it certifies nothing about a
	// SOURCE-side subject's own resolution.
	CensusMeasured bool
}

// RuleForms is the closed form vocabulary, in the order the arity error names
// them. Seven walk edges; seven read a member's own props.
var RuleForms = []RuleForm{
	{Key: "forbid", Subject: func(r ConstraintRule) string { return r.Forbid }, WalksEdges: true, Side: sourceSide},
	{Key: "forbid_reach", Subject: func(r ConstraintRule) string { return r.ForbidReach }, WalksEdges: true, Side: sourceSide},
	{Key: "allow", Subject: func(r ConstraintRule) string { return r.Allow }, WalksEdges: true, Side: sourceSide},
	{Key: "protect", Subject: func(r ConstraintRule) string { return r.Protect }, WalksEdges: true, Side: targetSide},
	{Key: "private", Subject: func(r ConstraintRule) string { return r.Private }, WalksEdges: true, Side: targetSide},
	{Key: "forbid_fact", Subject: func(r ConstraintRule) string { return r.ForbidFact }},
	{Key: "cap", Subject: func(r ConstraintRule) string { return r.Cap }},
	{Key: "require", Subject: func(r ConstraintRule) string { return r.Require }},
	{Key: "require_edge", Subject: func(r ConstraintRule) string { return r.RequireEdge }, WalksEdges: true, Side: requireEdgeSubjectSide, CensusMeasured: true},
	{Key: "require_defines", Subject: func(r ConstraintRule) string { return r.RequireDefines }},
	{Key: "forbid_cycles", Subject: func(r ConstraintRule) string { return r.ForbidCycles }},
	// independent reads its own members' edges against the includers resolved
	// ancestry names; no component resolves as the far end of a measured
	// relation, so it is not an edge-role form and the concept screen leaves it
	// to its own refusal.
	{Key: "independent", Subject: func(r ConstraintRule) string { return r.Independent }},
	{Key: "require_name", Subject: func(r ConstraintRule) string { return r.RequireName }},
	{Key: "forbid_name", Subject: func(r ConstraintRule) string { return r.ForbidName }},
	{Key: "protocol", Subject: func(r ConstraintRule) string { return r.Protocol }, WalksEdges: true, Side: sourceSide, CensusMeasured: true},
	{Key: "guide", Subject: func(r ConstraintRule) string { return r.Guide }},
	{Key: "storage_stays_home", Subject: func(r ConstraintRule) string { return r.StorageStaysHome }},
	{Key: "cap_runtime", Subject: func(r ConstraintRule) string { return r.CapRuntime }},
	{Key: "require_consumer", Subject: func(r ConstraintRule) string { return r.RequireConsumer }},
	{Key: "unique_across", Subject: func(r ConstraintRule) string { return r.UniqueAcross }},
	{Key: "require_governed", Subject: func(r ConstraintRule) string { return r.RequireGoverned }},
}

func sourceSide(ConstraintRule) string { return SideSource }

func targetSide(ConstraintRule) string { return SideTarget }

// requireEdgeSubjectSide reads the direction: inbound demands that some edge
// LAND on each member, so the member is the target; outbound demands the member
// make one, so it is the source.
func requireEdgeSubjectSide(r ConstraintRule) string {
	if r.Direction == "inbound" {
		return SideTarget
	}
	return SideSource
}

// CounterpartRole is a role a rule fills with a component OTHER than its
// subject. Every one of them is resolved against a measured edge — there is no
// counterpart role in this vocabulary that reads a member's own props — so the
// table carries no WalksEdges flag: it would be true in every row. Side is per
// role for the same reason it is per form: owners: names the sources allowed to
// reach a protected component, while only: names the landings an allowed
// component may reach, and one test cannot answer for both.
type CounterpartRole struct {
	Key   string
	Names func(ConstraintRule) []string
	Side  func(ConstraintRule) string
}

// CounterpartRoles is the closed counterpart vocabulary: the far end of a
// forbidden or demanded edge (to), the sources allowed to make one (owners,
// except), the landing sites allowed to receive one (only), and the ordered
// stages an edge must have reached (steps).
var CounterpartRoles = []CounterpartRole{
	{Key: "to", Names: func(r ConstraintRule) []string { return []string{r.To} }, Side: counterpartToSide},
	{Key: "owners", Names: func(r ConstraintRule) []string { return r.Owners }, Side: sourceSide},
	{Key: "only", Names: func(r ConstraintRule) []string { return r.Only }, Side: targetSide},
	{Key: "except", Names: func(r ConstraintRule) []string { return r.Except }, Side: sourceSide},
	{Key: "steps", Names: func(r ConstraintRule) []string { return r.Steps }, Side: targetSide},
}

// counterpartToSide is the mirror of the subject's: to: is the far end of the
// edge its form walks, so under forbid it is the target and under an inbound
// require_edge it is the SOURCE the demanded edge must come from.
func counterpartToSide(r ConstraintRule) string {
	if r.RequireEdge != "" && r.Direction == "inbound" {
		return SideSource
	}
	return SideTarget
}

// AllRuleVias is the rule-via vocabulary as an ordered slice — the same closed
// set AllowedRuleVias tests membership in, kept beside it so widening one
// widens the other in the same change.
var AllRuleVias = []string{"calls", "depends_on", "implements", "imports"}

// PathTargetVia names the edge kinds whose target is a PATH rather than a fact
// name in the general case, so a component can only be NAMED by one through the
// measured file it resolves to. Imports is the whole set: a depends_on target
// is a path only when the fact declaring it was declared in markup, which is a
// property of the fact rather than of the vocabulary.
func PathTargetVia(via string) bool { return via == "imports" }

// RuleVias lists the edge kinds one rule traverses. Most forms carry one;
// private carries none of its own and walks the whole rule-via vocabulary,
// which forbid_reach also does when it declares no via.
func RuleVias(r ConstraintRule) []string {
	if r.Private != "" || r.Independent != "" || (r.ForbidReach != "" && r.Via == "") {
		return AllRuleVias
	}
	if r.Via == "" {
		return nil
	}
	return []string{r.Via}
}

// EdgeRole is one component a rule resolves against a measured edge, with the
// declaration field that put it there and the end of the edge it resolves
// against. It is the one place role participation is decided, so a reading that
// has to hold for every role is written once rather than once per form.
type EdgeRole struct {
	Component string
	Role      string
	Side      string
	// Subject marks the form's own key, as against a counterpart role.
	Subject bool
	// CensusMeasured carries the form's flag onto its subject role only, since a
	// counterpart is never what the extraction census answers for. It is read
	// together with Side: the census answers for the landing end alone.
	CensusMeasured bool
	// Breaches marks the roles whose empty resolution the rule reads as a
	// POSITIVE verdict rather than as nothing to say. An owners: that owns no
	// edge makes every edge unowned; an only: that absorbs no landing makes
	// every landing disallowed; an except: that excepts nothing makes every
	// reach a trespass; a protocol step nothing reaches makes every later step a
	// skipped prerequisite; a require_edge to: narrows what would have satisfied
	// the demand, so an empty one breaches every member. Those roles manufacture
	// breaches out of an empty resolution, and one edge kind they cannot reach
	// is enough to poison them. The rest read an empty resolution as no verdict,
	// so a rule unreachable on one of several kinds still judged the others.
	Breaches bool
}

// EdgeRoles lists every component this rule resolves against a measured edge,
// in form order then counterpart order. The five forms that walk no edge
// contribute nothing: forbid_fact, cap, require, require_defines and
// require_name read a member's own props, and guide judges nothing.
func EdgeRoles(r ConstraintRule) []EdgeRole {
	var out []EdgeRole
	for _, form := range RuleForms {
		if !form.WalksEdges {
			continue
		}
		if name := form.Subject(r); name != "" {
			out = append(out, EdgeRole{Component: name, Role: form.Key, Side: form.Side(r), Subject: true, CensusMeasured: form.CensusMeasured, Breaches: form.Key == "require_edge"})
		}
	}
	for _, role := range CounterpartRoles {
		for _, name := range role.Names(r) {
			if name != "" {
				out = append(out, EdgeRole{Component: name, Role: role.Key, Side: role.Side(r), Breaches: counterpartBreaches(role.Key, r)})
			}
		}
	}
	return out
}

// counterpartBreaches decides the flag per counterpart role. to: is the one
// that differs by form: under forbid and forbid_reach an unresolved far end
// simply finds no edge, while under require_edge it narrows what would have
// satisfied the demand, so every member breaches.
func counterpartBreaches(role string, r ConstraintRule) bool {
	if role == "to" {
		return r.RequireEdge != ""
	}
	return true
}

func ruleFormKeys() string {
	keys := make([]string, 0, len(RuleForms))
	for _, form := range RuleForms {
		keys = append(keys, form.Key)
	}
	return strings.Join(keys, ", ")
}

// membershipFormKeys names the forms a predicate component may still be the
// subject of, for the errors below to point at.
func membershipFormKeys() string {
	var keys []string
	for _, form := range RuleForms {
		if !form.WalksEdges {
			keys = append(keys, form.Key)
		}
	}
	return strings.Join(keys, ", ")
}

// edgeRoleProblems screens, at declaration time, every rule that puts a
// component carrying a where predicate into a role resolved against a measured
// edge — per role, per side, and per edge kind.
//
// A predicate selects the facts that CARRY a property, and the properties this
// vocabulary can test are measured on the class. The call graph connects
// METHODS: in Ruby a class's calls ride its Owner#method facts, which carry
// none of those props and therefore cannot be members. That is why a component
// needs a declared ownership before it may be party to an edge at all — the
// verdict has to say whether those methods' edges are the class's, and no
// selector states it. A rule refused here never compiles into a fact, so no
// such rule reaches the explainer.
//
// Two refusals survive the ownership declaration, both of them statements about
// what this vocabulary CAN say rather than about any snapshot:
//
//   - A predicate component cannot SOURCE an imports edge. Every imports edge
//     rides a dependency fact, a dependency fact carries none of the props a
//     predicate tests (measured on the monolith: zero of superclass,
//     symbol_kind, storage_kind, cyclomatic), and no ownership in this
//     vocabulary reaches a file's dependency facts. The reach that would is the
//     file-hosting carrier, which is held out.
//   - A predicate component cannot BE NAMED by an imports edge unless it can
//     ground: an imports target names a path, grounding joins the component's
//     match globs, and it refuses a name-narrowed component outright. Without
//     match patterns, or with a name_pattern, the target resolves against
//     nothing and the rule reads that nothing as a verdict.
func edgeRoleProblems(loc string, r ConstraintRule, components map[string]ConstraintComponent, noun string) []string {
	roles := EdgeRoles(r)
	if len(roles) == 0 {
		return nil
	}
	overrides := r.ownershipOverrides()
	vias := RuleVias(r)
	var problems []string
	for _, role := range roles {
		c, declared := components[role.Component]
		if !declared || len(c.Predicate()) == 0 {
			continue
		}
		// A component declaring kind: symbol selects the Owner#method facts
		// themselves, so in a SUBJECT role its members are the edge carriers
		// and there is no enclosed fact for an ownership declaration to speak
		// about: the question cannot arise, so demanding an answer would be
		// ceremony. A counterpart role is different and stays screened, because
		// it resolves against the far end of a measured edge, which is a path
		// whatever the component's granularity.
		if c.Kind == "symbol" && role.Subject {
			continue
		}
		if _, stated := OwnershipPrecedence(c.Owns, overrides[role.Component]); !stated {
			problems = append(problems, fmt.Sprintf("%s (%s): %s %q is selected by a where predicate and sits in the %s role, and nothing declares what it owns — a predicate selects the facts that carry a property, while a class's calls ride its Owner#method facts, so a verdict about that edge has to state whether its members' methods are the member's; declare owns: (%s) on the %s, or an owns: override on this rule, or bind this role to a match or service %s and keep the predicate for the forms that read a member's own props (%s)",
				loc, r.ID, noun, role.Component, role.Role, allowedOwnerships(), noun, noun, membershipFormKeys()))
			continue
		}
		for _, via := range vias {
			if !PathTargetVia(via) {
				continue
			}
			if role.Side == SideSource {
				problems = append(problems, fmt.Sprintf("%s (%s): %s %q is selected by a where predicate and cannot sit in the %s role of a rule walking %s — every %s edge rides a dependency fact, which carries none of the props a predicate tests, and no ownership reaches a file's dependency facts; bind this role to a match or service %s instead",
					loc, r.ID, noun, role.Component, role.Role, via, via, noun))
				continue
			}
			if len(c.Match) == 0 || c.NamePattern != "" {
				problems = append(problems, fmt.Sprintf("%s (%s): %s %q is selected by a where predicate and cannot sit in the %s role of a rule walking %s — an %s target names a path, so it reaches a component only through the measured file it names, and that join needs match patterns and refuses a name_pattern; add match patterns to the %s and drop any name_pattern, or bind this role to a path %s instead",
					loc, r.ID, noun, role.Component, role.Role, via, via, noun, noun))
			}
		}
	}
	sort.Strings(problems)
	return problems
}
