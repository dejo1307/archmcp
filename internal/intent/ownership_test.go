package intent

import (
	"strings"
	"testing"
)

func ownershipDeclaration(component ConstraintComponent, r ConstraintRule) *Declaration {
	return &Declaration{
		Components: []ConstraintComponent{
			component,
			{Name: pathComponent, Match: []string{"app/**"}},
		},
		Rules: []ConstraintRule{base(r)},
	}
}

// The precedence is stated once and pinned in both directions, so a later
// change cannot quietly invert it: the rule's override wins whether it is more
// permissive than the component's declaration or stricter.
func TestOwnership_PrecedenceRunsRuleOverComponentInBothDirections(t *testing.T) {
	widened, declared := OwnershipPrecedence(OwnsNothing, OwnsMethods)
	if widened != OwnsMethods || !declared {
		t.Errorf("OwnershipPrecedence(nothing, methods) = %q/%v, want the rule's more permissive answer", widened, declared)
	}
	narrowed, declared := OwnershipPrecedence(OwnsMethods, OwnsNothing)
	if narrowed != OwnsNothing || !declared {
		t.Errorf("OwnershipPrecedence(methods, nothing) = %q/%v, want the rule's stricter answer", narrowed, declared)
	}
	component, declared := OwnershipPrecedence(OwnsMethods, "")
	if component != OwnsMethods || !declared {
		t.Errorf("OwnershipPrecedence(methods, unset) = %q/%v, want the component's own answer", component, declared)
	}
	unstated, declared := OwnershipPrecedence("", "")
	if unstated != OwnsNothing || declared {
		t.Errorf("OwnershipPrecedence(unset, unset) = %q/%v, want nothing and UNDECLARED — an absent ownership is not an explicit one", unstated, declared)
	}
}

// Two rule-level answers for one component have no precedence between them.
// The two-place shape makes that reachable, so it is a named error rather than
// a silent last-one-wins.
func TestOwnership_TwoOverridesForOneComponentAreAmbiguousAndNamed(t *testing.T) {
	d := ownershipDeclaration(
		ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "StandardError"}},
		ConstraintRule{Forbid: predicateComponent, To: pathComponent, Via: "calls", Owns: []ComponentOwnership{
			{Component: predicateComponent, Owns: OwnsMethods},
			{Component: predicateComponent, Owns: OwnsNothing},
		}})
	problems := d.Problems()
	found := false
	for _, p := range problems {
		if strings.Contains(p, "already given an ownership by this rule") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want the ambiguity named", problems)
	}
	if err := d.Validate(); err == nil {
		t.Fatal("an ambiguous declaration must not validate")
	}
}

// An override for a component the rule does not name overrides nothing, and a
// silent no-op is how a reader comes to believe a reach was declared.
func TestOwnership_AnOverrideMustNameAComponentTheRuleNames(t *testing.T) {
	d := ownershipDeclaration(
		ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "StandardError"}, Owns: OwnsMethods},
		ConstraintRule{ForbidFact: pathComponent, Owns: []ComponentOwnership{{Component: predicateComponent, Owns: OwnsMethods}}})
	problems := d.Problems()
	for _, p := range problems {
		if strings.Contains(p, "is not named by this rule") {
			return
		}
	}
	t.Fatalf("problems = %v, want the unreachable override named", problems)
}

func TestOwnership_ValueComesFromTheClosedVocabulary(t *testing.T) {
	d := ownershipDeclaration(
		ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "StandardError"}, Owns: "everything"},
		ConstraintRule{ForbidFact: predicateComponent})
	problems := d.Problems()
	for _, p := range problems {
		if strings.Contains(p, `owns "everything" is not an ownership`) {
			return
		}
	}
	t.Fatalf("problems = %v, want the value refused against the closed vocabulary", problems)
}

// Ownership is not a selector, and the one field that widens must not read as
// licence to widen one. A path component already contains the facts in the
// files it names, so ownership there would widen nothing while looking like it
// did.
func TestOwnership_BelongsOnlyToAComponentAPredicateSelects(t *testing.T) {
	d := ownershipDeclaration(
		ConstraintComponent{Name: predicateComponent, Match: []string{"app/errors/**"}, Owns: OwnsMethods},
		ConstraintRule{ForbidFact: predicateComponent})
	problems := d.Problems()
	for _, p := range problems {
		if strings.Contains(p, "owns belongs to a component selected by a where predicate") {
			return
		}
	}
	t.Fatalf("problems = %v, want ownership refused on a path component", problems)
}

// The compiled prop round-trips as a set, so the fact — and every fingerprint
// downstream of it — is a function of the declaration rather than of YAML order.
func TestOwnership_EncodesAsASortedSetAndDecodesBack(t *testing.T) {
	encoded := EncodeOwnership([]ComponentOwnership{
		{Component: "views", Owns: OwnsNothing},
		{Component: "exceptions", Owns: OwnsMethods},
	})
	if encoded != "exceptions=methods views=nothing" {
		t.Fatalf("EncodeOwnership = %q, want the sorted set", encoded)
	}
	decoded := DecodeOwnership(encoded)
	if decoded["exceptions"] != OwnsMethods || decoded["views"] != OwnsNothing || len(decoded) != 2 {
		t.Fatalf("DecodeOwnership = %v, want both pairs", decoded)
	}
}

// A field that did not survive compilation must leave the component's own
// declaration standing, which is the narrower reading. Decoding it as an
// ownership the declaration never stated is the one direction this vocabulary
// must never fail in.
func TestOwnership_AnUndecodableFieldIsDroppedRatherThanGuessed(t *testing.T) {
	if got := DecodeOwnership("exceptions=everything"); got != nil {
		t.Fatalf("DecodeOwnership = %v, want nothing: the value is outside the vocabulary", got)
	}
	if got := DecodeOwnership("exceptions"); got != nil {
		t.Fatalf("DecodeOwnership = %v, want nothing: the field is not a pair", got)
	}
}
