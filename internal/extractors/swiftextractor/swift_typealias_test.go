package swiftextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// TestTypeAlias_EmitsReferenceEdgeToUnderlyingType pins GAP-SW-09: a type reached
// only through a `typealias` name must still credit the underlying declaration, or
// the dead-code detector reports it as an unreferenced orphan. handleTypeAlias
// folds the aliased type in as an instantiation edge on the alias fact.
func TestTypeAlias_EmitsReferenceEdgeToUnderlyingType(t *testing.T) {
	ff := extractAST(t, `
class AliasTarget {
    func perform() {}
}
typealias AliasName = AliasTarget
`, false)

	f, ok := findFact(ff, "pkg.AliasName")
	if !ok {
		t.Fatal("expected fact for pkg.AliasName")
	}
	if !hasRelation(f, facts.RelInstantiates, "AliasTarget") {
		t.Errorf("expected typealias to reference underlying type AliasTarget; relations=%v", f.Relations)
	}
}

// TestTypeAlias_FunctionTypeRHSEmitsNoEdge guards the fold: a function-type,
// tuple, or otherwise unresolvable RHS yields no simple type name, so the alias
// fact must carry no spurious reference edge.
func TestTypeAlias_FunctionTypeRHSEmitsNoEdge(t *testing.T) {
	ff := extractAST(t, `
typealias Handler = (Int) -> Void
`, false)

	f, ok := findFact(ff, "pkg.Handler")
	if !ok {
		t.Fatal("expected fact for pkg.Handler")
	}
	for _, r := range f.Relations {
		if r.Kind == facts.RelInstantiates || r.Kind == facts.RelCalls {
			t.Errorf("did not expect a reference edge for a function-type alias; got %v", r)
		}
	}
}
