package constraints

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// The evaluator's half of the one-component-index pin. The screen refuses a
// duplicate name outright, so a second fact for one name reaches this loop only
// from a store the screen never passed — and the answer it gives must still be
// the screen's FIRST-wins, or a declaration compiles under one reading and
// verdicts under the other. That divergence is what let two inline components
// declare owns: methods and owns: nothing and produce a breach nobody declared.
func TestComponentCollision_TheEvaluatorKeepsTheFirstDeclaration(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("exceptions", map[string]string{"superclass": "StandardError"},
			map[string]any{"owns": intent.OwnsMethods}),
		predicateComponentIntent("exceptions", map[string]string{"superclass": "StandardError"},
			map[string]any{"owns": intent.OwnsNothing}),
	)
	components, _ := declarations(store)
	if got := components["exceptions"].owns; got != intent.OwnsMethods {
		t.Fatalf("the evaluator resolved owns %q, want the FIRST declaration's %q — both halves resolve one component index the same way", got, intent.OwnsMethods)
	}
}
