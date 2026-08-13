package intent

import (
	"fmt"
	"strings"
)

// The form vocabulary, written once. Every surface that has to answer "which
// forms are there" reads these two tables instead of repeating the list, so a
// form added to ConstraintRule without an entry here is missing from the arity
// message, from the undeclared-component check and from the predicate screen at
// the same time — which the enumeration test in ruleforms_test.go turns into a
// failure rather than a silent gap.

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
}

// RuleForms is the closed form vocabulary, in the order the arity error names
// them. Seven walk edges; six read a member's own props.
var RuleForms = []RuleForm{
	{Key: "forbid", Subject: func(r ConstraintRule) string { return r.Forbid }, WalksEdges: true},
	{Key: "forbid_reach", Subject: func(r ConstraintRule) string { return r.ForbidReach }, WalksEdges: true},
	{Key: "allow", Subject: func(r ConstraintRule) string { return r.Allow }, WalksEdges: true},
	{Key: "protect", Subject: func(r ConstraintRule) string { return r.Protect }, WalksEdges: true},
	{Key: "private", Subject: func(r ConstraintRule) string { return r.Private }, WalksEdges: true},
	{Key: "forbid_fact", Subject: func(r ConstraintRule) string { return r.ForbidFact }},
	{Key: "cap", Subject: func(r ConstraintRule) string { return r.Cap }},
	{Key: "require", Subject: func(r ConstraintRule) string { return r.Require }},
	{Key: "require_edge", Subject: func(r ConstraintRule) string { return r.RequireEdge }, WalksEdges: true},
	{Key: "require_defines", Subject: func(r ConstraintRule) string { return r.RequireDefines }},
	{Key: "require_name", Subject: func(r ConstraintRule) string { return r.RequireName }},
	{Key: "protocol", Subject: func(r ConstraintRule) string { return r.Protocol }, WalksEdges: true},
	{Key: "guide", Subject: func(r ConstraintRule) string { return r.Guide }},
}

// CounterpartRole is a role a rule fills with a component OTHER than its
// subject. Every one of them is resolved against a measured edge — there is no
// counterpart role in this vocabulary that reads a member's own props — so the
// table carries no WalksEdges flag: it would be true in every row.
type CounterpartRole struct {
	Key   string
	Names func(ConstraintRule) []string
}

// CounterpartRoles is the closed counterpart vocabulary: the far end of a
// forbidden or demanded edge (to), the sources allowed to make one (owners,
// except), the landing sites allowed to receive one (only), and the ordered
// stages an edge must have reached (steps).
var CounterpartRoles = []CounterpartRole{
	{Key: "to", Names: func(r ConstraintRule) []string { return []string{r.To} }},
	{Key: "owners", Names: func(r ConstraintRule) []string { return r.Owners }},
	{Key: "only", Names: func(r ConstraintRule) []string { return r.Only }},
	{Key: "except", Names: func(r ConstraintRule) []string { return r.Except }},
	{Key: "steps", Names: func(r ConstraintRule) []string { return r.Steps }},
}

func ruleFormKeys() string {
	keys := make([]string, 0, len(RuleForms))
	for _, form := range RuleForms {
		keys = append(keys, form.Key)
	}
	return strings.Join(keys, ", ")
}

// membershipFormKeys names the forms a predicate component may still be the
// subject of, for the error below to point at.
func membershipFormKeys() string {
	var keys []string
	for _, form := range RuleForms {
		if !form.WalksEdges {
			keys = append(keys, form.Key)
		}
	}
	return strings.Join(keys, ", ")
}

// predicateRoleProblems refuses, at declaration time, every rule that puts a
// component carrying a where predicate into a role resolved against a measured
// edge.
//
// A predicate selects the facts that CARRY a property, and the properties this
// vocabulary can test — superclass, symbol_kind, storage_kind, framework,
// cyclomatic — are measured on the CLASS. The call graph connects METHODS: in
// Ruby a class's calls ride its Owner#method facts, which carry none of those
// props and therefore cannot be members. So an edge-walking rule resolves a
// predicate component against nothing at its source side, and against a path
// rather than a fact name at its target side — and every role reads one of
// those two. The rule then reads that nothing as a verdict: an owners: that owns
// no edge makes every edge unowned, an only: that absorbs no landing makes every
// landing disallowed, and a subject that sources no edge reads as a clean pass.
//
// Making the edge forms honest needs Graph.methodOwner to learn the "#"
// separator AND a declared notion of member ownership — that a class's methods'
// edges are the class's edges — which this vocabulary does not have. Until both
// land, the only honest answer at an edge-walking role is refusal, and the
// cheapest correct refusal is at authoring time: a rule refused here never
// compiles into a fact, so no such rule reaches the explainer at all. That is
// what makes the screen total, rather than a verdict-site patch that has to be
// right in seven forms and five counterpart roles independently.
// The one exemption is the require_edge SUBJECT bound to a symbol-granular
// component. The refusal's premise is that a predicate names class facts while
// the call graph connects methods; a component declaring kind: symbol selects
// the Owner#method facts themselves, so its members ARE the edge carriers and
// the mismatch the refusal exists for cannot arise. It is scoped to that one
// role because that is the one whose mechanics this claim was checked against:
// the outbound verdict reads each member fact's own Relations, so a
// symbol-granular member answers for itself. Every counterpart role still
// resolves a component against the far end of a measured edge — a path, not a
// fact name — and stays refused, as do the subject roles of the forms that read
// their members as edge TARGETS rather than sources.
func predicateRoleProblems(loc string, r ConstraintRule, predicated, symbolGranular map[string]bool, noun string) []string {
	if len(predicated) == 0 {
		return nil
	}
	var problems []string
	refuse := func(role, name string) {
		if name == "" || !predicated[name] {
			return
		}
		if symbolGranular[name] && (role == "require_edge" || role == "forbid") {
			return
		}
		problems = append(problems, fmt.Sprintf("%s (%s): %s %q is selected by a where predicate and cannot sit in the %s role — a predicate selects the facts that carry a property, and a class's calls ride its Owner#method facts, so an edge-walking rule resolves it against nothing; bind this role to a match or service %s instead, and keep the predicate for the forms that read a member's own props (%s)",
			loc, r.ID, noun, name, role, noun, membershipFormKeys()))
	}
	for _, form := range RuleForms {
		if form.WalksEdges {
			refuse(form.Key, form.Subject(r))
		}
	}
	for _, role := range CounterpartRoles {
		for _, name := range role.Names(r) {
			refuse(role.Key, name)
		}
	}
	return problems
}
