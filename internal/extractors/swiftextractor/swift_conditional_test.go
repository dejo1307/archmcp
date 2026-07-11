package swiftextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// factsNamed returns every fact with the given name (unlike findFact, which stops
// at the first). A #if/#else double emission produces two same-name facts, so the
// conditional-compilation tests need to inspect the whole group.
func factsNamed(ff []facts.Fact, name string) []facts.Fact {
	var out []facts.Fact
	for _, f := range ff {
		if f.Name == name {
			out = append(out, f)
		}
	}
	return out
}

// TestConditionalCompilation_TagsBothBranches asserts that a type declared once in
// each branch of a #if/#else conditional-compilation block carries conditional=true
// on BOTH emitted facts. Tree-sitter walks both branches (it does not evaluate the
// compile-time condition), so the type yields two same-name symbol facts; tagging
// them lets consumers group/dedupe what is, in any single build, one type. GAP-SW-10.
func TestConditionalCompilation_TagsBothBranches(t *testing.T) {
	ff := extractAST(t, `
#if os(macOS)
final class Gate {
    func runFull() {}
}
#else
final class Gate {
    func runSkipped() {}
}
#endif
`, false)

	gates := factsNamed(ff, "pkg.Gate")
	if len(gates) != 2 {
		t.Fatalf("expected 2 pkg.Gate facts (one per branch), got %d", len(gates))
	}
	for _, f := range gates {
		if v, _ := f.Props["conditional"].(bool); !v {
			t.Errorf("branch class at line %d must carry conditional=true; props=%v", f.Line, f.Props)
		}
	}

	// Members of a conditional type are themselves conditional.
	for _, m := range []string{"pkg.Gate.runFull", "pkg.Gate.runSkipped"} {
		f, ok := findFact(ff, m)
		if !ok {
			t.Fatalf("expected fact for %s", m)
		}
		if v, _ := f.Props["conditional"].(bool); !v {
			t.Errorf("member %s of a conditional type must carry conditional=true; props=%v", m, f.Props)
		}
	}
}

// TestConditionalCompilation_UnconditionalNotTagged asserts the prop is a positive
// signal only: a type outside any directive must not carry it.
func TestConditionalCompilation_UnconditionalNotTagged(t *testing.T) {
	ff := extractAST(t, `
final class Plain {
    func run() {}
}
`, false)

	for _, n := range []string{"pkg.Plain", "pkg.Plain.run"} {
		f, ok := findFact(ff, n)
		if !ok {
			t.Fatalf("expected fact for %s", n)
		}
		if _, present := f.Props["conditional"]; present {
			t.Errorf("unconditional symbol %s must not carry conditional prop; props=%v", n, f.Props)
		}
	}
}

// TestConditionalCompilation_MemberLevelDirective asserts a #if INSIDE a type body
// tags only the guarded member — the enclosing type and its unguarded siblings stay
// untagged.
func TestConditionalCompilation_MemberLevelDirective(t *testing.T) {
	ff := extractAST(t, `
final class Host {
    func always() {}
#if DEBUG
    func debugOnly() {}
#endif
}
`, false)

	if f, ok := findFact(ff, "pkg.Host"); !ok {
		t.Fatal("expected fact for pkg.Host")
	} else if _, present := f.Props["conditional"]; present {
		t.Errorf("enclosing type must not be conditional; props=%v", f.Props)
	}
	if f, ok := findFact(ff, "pkg.Host.always"); !ok {
		t.Fatal("expected fact for pkg.Host.always")
	} else if _, present := f.Props["conditional"]; present {
		t.Errorf("unguarded sibling must not be conditional; props=%v", f.Props)
	}
	if f, ok := findFact(ff, "pkg.Host.debugOnly"); !ok {
		t.Fatal("expected fact for pkg.Host.debugOnly")
	} else if v, _ := f.Props["conditional"].(bool); !v {
		t.Errorf("guarded member must carry conditional=true; props=%v", f.Props)
	}
}
