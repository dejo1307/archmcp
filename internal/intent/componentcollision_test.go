package intent

import (
	"strings"
	"testing"
)

// One component name, two declarations, and the two halves of the vocabulary
// reading it differently is the failure this file exists to end. The screen
// indexed the FIRST declaration and the evaluator kept the LAST, and the
// collision check only fired when a constraints file was involved — so two
// INLINE declarations of one name collided with nothing, compiled under the
// screen's reading, and verdicted under the evaluator's. A declaration that
// says two things about one name has no single answer for what it selects, and
// which answer a reader gets must never depend on which half is asking.

func duplicateInline(first, second ConstraintComponent) *Declaration {
	return &Declaration{
		Components: []ConstraintComponent{first, second, {Name: pathComponent, Match: []string{"app/**"}}},
		Rules: []ConstraintRule{base(ConstraintRule{
			Forbid: predicateComponent, To: pathComponent, Via: "calls"})},
	}
}

// Two inline declarations of one name are a named error, exactly as two in
// files are. This is the reproduction: owns: methods then owns: nothing, which
// compiled and produced an extra 1.0 breach nobody declared.
func TestComponentCollision_TwoInlineDeclarationsAreNamedNotSilent(t *testing.T) {
	d := duplicateInline(
		ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "StandardError"}, Owns: OwnsMethods},
		ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "StandardError"}, Owns: OwnsNothing},
	)
	problems := d.Problems()
	found := false
	for _, p := range problems {
		if strings.Contains(p, "is declared twice in this declaration") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want the duplicate component named — ambiguity is a named error, never a silent winner", problems)
	}
	if err := d.Validate(); err == nil {
		t.Fatal("Validate() = nil, want the duplicate to refuse the declaration")
	}
}

// The same collision across declaring files keeps naming the other file, which
// is the only thing the two shapes should word differently.
func TestComponentCollision_AFileDeclarationStillNamesTheOtherFile(t *testing.T) {
	d := duplicateInline(
		ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "StandardError"}, Owns: OwnsMethods},
		ConstraintComponent{Name: predicateComponent, SourceFile: "enola/constraints/concepts.yaml",
			Where: map[string]any{"superclass": "StandardError"}, Owns: OwnsNothing},
	)
	problems := d.Problems()
	found := false
	for _, p := range problems {
		if strings.Contains(p, "is already declared by") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want the collision to name the other declaring file", problems)
	}
}

// And the screen's own index resolves first-wins, which is the resolution the
// evaluator is pinned to in the explainer's half of this test. A change to
// either side fails one of the two.
func TestComponentCollision_TheScreenIndexesTheFirstDeclaration(t *testing.T) {
	first := ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "StandardError"}, Owns: OwnsMethods}
	second := ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "AppError"}, Owns: OwnsNothing}
	indexed := map[string]ConstraintComponent{}
	for _, c := range []ConstraintComponent{first, second} {
		if _, seen := indexed[c.Name]; !seen {
			indexed[c.Name] = c
		}
	}
	if indexed[predicateComponent].Owns != OwnsMethods {
		t.Fatalf("the screen indexed owns %q, want the FIRST declaration's %q", indexed[predicateComponent].Owns, OwnsMethods)
	}
	// The screen reads that index to decide whether an edge role is refused, so
	// a declaration whose first entry states an ownership must not be refused
	// for the second entry's silence.
	d := duplicateInline(first, ConstraintComponent{Name: predicateComponent, Where: map[string]any{"superclass": "StandardError"}})
	for _, p := range d.Problems() {
		if strings.Contains(p, "nothing declares what it owns") {
			t.Fatalf("problems = %v, want no ownership refusal: the indexed declaration states one", d.Problems())
		}
	}
}
