package intent

import (
	"fmt"
	"sort"
	"strings"
)

// Ownership is the one statement a component makes about facts that are not its
// members: whether its members' METHODS count as the member's. It is the
// missing semantic behind every edge form. A predicate selects the facts that
// CARRY a property — for superclass, the class facts — while the call graph
// joins methods, which Ruby names `Owner#method`. Whether a class's methods'
// edges are the class's edges is a choice about what a component MEANS, and
// until the declaration makes it, a verdict about such an edge asserts a reach
// nobody declared.
//
// Ownership is declared in TWO places with a stated precedence, and the
// precedence is stated exactly once — in OwnershipPrecedence below. The
// component carries the concept's default so a reader has one place to learn
// what it means; a rule may override it for its own reach, so one concept
// governed by two laws with different reach is written once and read twice. A
// declaration that is ambiguous under the precedence — two rule-level answers
// for one component — is a named error rather than a silent winner.

// The closed ownership vocabulary. Nothing is the default and means a member's
// own facts and nothing else; methods adds the member's methods, and exactly
// those — the facts the graph's has_method edges reach, which it wires for
// method, function and getter symbols. Ownership reaches no further into a
// member's body: a constant or a nested class written inside a member is NOT
// owned, because no has_method edge names it and this vocabulary measures
// nothing else. A rule walking an edge that lands on such a constant therefore
// lands outside the component, which is a true breach rather than a missed
// ownership.
const (
	OwnsNothing = "nothing"
	OwnsMethods = "methods"
)

// AllowedOwnerships is the closed vocabulary an owns field may name.
var AllowedOwnerships = map[string]bool{OwnsNothing: true, OwnsMethods: true}

func allowedOwnerships() string { return "methods, nothing" }

// ComponentOwnership is one rule-level override: what a component owns for the
// reach of this rule alone. The component is named explicitly rather than
// applied to every component the rule mentions, because a rule naming two
// concepts governs them separately and a blanket override would state something
// about the second that nobody wrote.
type ComponentOwnership struct {
	Component string `yaml:"component"`
	Owns      string `yaml:"owns"`
}

// OwnershipPrecedence is the ONE statement of precedence in this vocabulary:
// the rule's override wins over the component's own declaration, and a
// component neither declares is undeclared rather than defaulted silently. The
// second return value is what an edge role screens on — an undeclared ownership
// is a component whose meaning at an edge is unstated, which is refused rather
// than guessed.
//
// Every call site reads this function. Re-deriving the order per site is
// exactly how a precedence rule comes to resolve one way while a sentence
// claims another, which is the failure this work exists to end.
func OwnershipPrecedence(componentOwns, ruleOverride string) (owns string, declared bool) {
	if ruleOverride != "" {
		return ruleOverride, true
	}
	if componentOwns != "" {
		return componentOwns, true
	}
	return OwnsNothing, false
}

// EncodeOwnership renders a rule's overrides as the compiled fact's owns prop:
// component=value pairs sorted and space-joined, so the fact is a function of
// the declared SET rather than of the YAML order. Both halves are validated
// tokens carrying no whitespace and no `=`, so the split is unambiguous and
// needs none of the escaping EncodeWhere does.
func EncodeOwnership(owned []ComponentOwnership) string {
	if len(owned) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(owned))
	for _, o := range owned {
		pairs = append(pairs, o.Component+"="+o.Owns)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, " ")
}

// DecodeOwnership reads the compiled owns prop back into the per-component
// overrides the evaluator applies. A field that does not split into a pair is
// dropped: an override that did not survive compilation must leave the
// component's own declaration standing, which is the narrower reading, rather
// than compile into an ownership the declaration never stated.
func DecodeOwnership(encoded string) map[string]string {
	if encoded == "" {
		return nil
	}
	out := map[string]string{}
	for _, field := range strings.Fields(encoded) {
		component, owns, found := strings.Cut(field, "=")
		if !found || component == "" || !AllowedOwnerships[owns] {
			continue
		}
		out[component] = owns
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ownershipProblems validates one rule's overrides: each names a declared
// component the rule actually names, carries a value from the closed
// vocabulary, and appears once. The duplicate is the ambiguity the two-place
// shape makes reachable — two rule-level answers for one component have no
// precedence between them — and it is a named error rather than a silent
// last-one-wins.
func ownershipProblems(loc string, r ConstraintRule, componentNames map[string]bool, noun string) []string {
	if len(r.Owns) == 0 {
		return nil
	}
	named := map[string]bool{}
	for _, name := range r.componentNames() {
		named[name] = true
	}
	seen := map[string]bool{}
	var problems []string
	for i, o := range r.Owns {
		entry := fmt.Sprintf("%s (%s): owns[%d]", loc, r.ID, i)
		switch {
		case o.Component == "":
			problems = append(problems, fmt.Sprintf("%s: needs a component — an override with no subject overrides nothing", entry))
		case !componentNames[o.Component]:
			problems = append(problems, fmt.Sprintf("%s: component %q names no declared %s", entry, o.Component, noun))
		case !named[o.Component]:
			problems = append(problems, fmt.Sprintf("%s: component %q is not named by this rule, so this rule has no reach to override", entry, o.Component))
		case seen[o.Component]:
			problems = append(problems, fmt.Sprintf("%s: component %q is already given an ownership by this rule — precedence runs rule over component, and two rule-level answers for one component have no precedence between them", entry, o.Component))
		}
		seen[o.Component] = true
		if o.Owns != "" && !AllowedOwnerships[o.Owns] {
			problems = append(problems, fmt.Sprintf("%s: owns %q is not an ownership (allowed: %s)", entry, o.Owns, allowedOwnerships()))
		}
		if o.Owns == "" {
			problems = append(problems, fmt.Sprintf("%s: needs an owns (allowed: %s) — an override that states no ownership states nothing", entry, allowedOwnerships()))
		}
	}
	return problems
}

// componentOwnershipProblems validates the component half: a value from the
// closed vocabulary, and only on a component a predicate selects. A path
// component already contains every fact in the files it names, so ownership
// there would widen nothing and read as if it did.
func componentOwnershipProblems(loc string, c ConstraintComponent) []string {
	if c.Owns == "" {
		return nil
	}
	var problems []string
	if !AllowedOwnerships[c.Owns] {
		problems = append(problems, fmt.Sprintf("%s (%s): owns %q is not an ownership (allowed: %s)", loc, c.Name, c.Owns, allowedOwnerships()))
	}
	if len(c.Predicate()) == 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): owns belongs to a component selected by a where predicate — a component selected by path already contains the facts in the files it names, so ownership would widen nothing", loc, c.Name))
	}
	return problems
}

// ownershipOverrides indexes one rule's overrides by component, for the screen
// and the compiler to read the same way the evaluator will.
func (r ConstraintRule) ownershipOverrides() map[string]string {
	if len(r.Owns) == 0 {
		return nil
	}
	out := make(map[string]string, len(r.Owns))
	for _, o := range r.Owns {
		if o.Component != "" && AllowedOwnerships[o.Owns] {
			out[o.Component] = o.Owns
		}
	}
	return out
}

// componentNames lists every component this rule names, in form order then
// counterpart order — read off the two schema tables rather than from a list
// written here, so a form added without an entry names nothing rather than
// silently naming the wrong thing.
func (r ConstraintRule) componentNames() []string {
	var out []string
	for _, form := range RuleForms {
		if name := form.Subject(r); name != "" {
			out = append(out, name)
		}
	}
	for _, role := range CounterpartRoles {
		for _, name := range role.Names(r) {
			if name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}
