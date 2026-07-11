package facts

import "testing"

func condSym(name, file string, conditional bool) Fact {
	props := map[string]any{"symbol_kind": SymbolClass}
	if conditional {
		props["conditional"] = true
	}
	return Fact{Kind: KindSymbol, Name: name, File: file, Props: props}
}

// TestCanonicalSymbols_CollapsesConditionalDuplicates asserts a type declared in
// both branches of a #if/#else block (two same-name conditional facts in one file)
// is kept once, preserving the first.
func TestCanonicalSymbols_CollapsesConditionalDuplicates(t *testing.T) {
	in := []Fact{
		condSym("pkg.Gate", "pkg/Gate.swift", true),
		condSym("pkg.Gate", "pkg/Gate.swift", true),
	}
	out := CanonicalSymbols(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 canonical fact, got %d", len(out))
	}
	if out[0].Name != "pkg.Gate" {
		t.Errorf("expected pkg.Gate, got %s", out[0].Name)
	}
}

// TestCanonicalSymbols_KeepsNonConditionalOverloads asserts genuine overloads —
// same name, NOT tagged conditional — are never collapsed. The extractors emit
// method overloads under one base name, and they legitimately count separately.
func TestCanonicalSymbols_KeepsNonConditionalOverloads(t *testing.T) {
	in := []Fact{
		condSym("pkg.S.urlSession", "pkg/S.swift", false),
		condSym("pkg.S.urlSession", "pkg/S.swift", false),
		condSym("pkg.S.urlSession", "pkg/S.swift", false),
	}
	if out := CanonicalSymbols(in); len(out) != 3 {
		t.Fatalf("non-conditional overloads must be preserved; expected 3, got %d", len(out))
	}
}

// TestCanonicalSymbols_DifferentFilesNotCollapsed asserts two conditional facts of
// the same name in DIFFERENT files are distinct symbols and both kept (the key is
// (Name, File)).
func TestCanonicalSymbols_DifferentFilesNotCollapsed(t *testing.T) {
	in := []Fact{
		condSym("pkg.Widget", "pkg/A.swift", true),
		condSym("pkg.Widget", "pkg/B.swift", true),
	}
	if out := CanonicalSymbols(in); len(out) != 2 {
		t.Fatalf("same-name conditional facts in different files must both be kept; expected 2, got %d", len(out))
	}
}

// TestCanonicalSymbols_PreservesOrderAndMix asserts order is preserved and a mix of
// conditional duplicates and unrelated facts is handled correctly.
func TestCanonicalSymbols_PreservesOrderAndMix(t *testing.T) {
	in := []Fact{
		condSym("pkg.A", "pkg/A.swift", false),
		condSym("pkg.Gate", "pkg/Gate.swift", true),
		condSym("pkg.B", "pkg/B.swift", false),
		condSym("pkg.Gate", "pkg/Gate.swift", true),
		condSym("pkg.C", "pkg/C.swift", false),
	}
	out := CanonicalSymbols(in)
	want := []string{"pkg.A", "pkg.Gate", "pkg.B", "pkg.C"}
	if len(out) != len(want) {
		t.Fatalf("expected %d facts, got %d", len(want), len(out))
	}
	for i, n := range want {
		if out[i].Name != n {
			t.Errorf("position %d: expected %s, got %s", i, n, out[i].Name)
		}
	}
}
