package dartextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// funcProps returns the props of the first top-level function in src.
func funcProps(t *testing.T, src string) map[string]any {
	t.Helper()
	for _, f := range walkSource(t, "lib/a.dart", src) {
		if f.Kind == facts.KindSymbol && f.PropString("symbol_kind") == facts.SymbolFunc {
			return f.Props
		}
	}
	t.Fatalf("no function symbol extracted from:\n%s", src)
	return nil
}

// TestCyclomaticCountsDartOperators pins the operator counting, which was wrong in both
// directions before it was measured.
//
// Dart does not model `&&` as a generic binary expression: it has its own node kinds,
// and the OPERATOR is a node of its own. Matching a generic `binary_expression` counted
// nothing at all, so every logical operator in the corpus was invisible. Counting
// occurrences in the enclosing expression's TEXT would have been wrong the other way —
// `a && b && c` nests, so the outer node's operators would be recounted at each level.
// Counting operator nodes is exact.
func TestCyclomaticCountsDartOperators(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      int
	}{
		{"straight line", "int f() { return 1; }", 1},
		{"one and", "bool f(a, b) { return a && b; }", 2},
		{"chained ands do not double count", "bool f(a, b, c, d) { return a && b && c && d; }", 4},
		{"mixed and/or", "bool f(a, b, c) { return a && b || c; }", 3},
		{"null coalescing short-circuits", "int f(a, b) { return a ?? b; }", 2},
		{"ternary", "int f(a, b, c) { return a ? b : c; }", 2},
		{"if", "int f(a) { if (a) { return 1; } return 0; }", 2},
		{"switch arms", "String f(x) { switch (x) { case 1: return 'a'; case 2: return 'b'; } return ''; }", 3},
		{"catch", "void f() { try { g(); } catch (e) { h(); } }", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := funcProps(t, tc.src)["cyclomatic"].(int)
			if got != tc.want {
				t.Errorf("cyclomatic = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestConstantTripLoopDiscount pins the discount that keeps an honest O(n) from being
// reported as O(n²).
//
// A C-style `for` and a `for-in` are the SAME node kind in this grammar and are told
// apart only by their for_loop_parts — the C-style form carries a relational_expression
// condition, the for-in form carries none. Matching on node kinds that do not exist
// (which is what the first attempt did) makes the discount silently never apply, so
// every literal-bounded loop inflates the scaling depth the performance analyzer reads.
func TestConstantTripLoopDiscount(t *testing.T) {
	for _, tc := range []struct {
		name, src        string
		wantLoopDepth    int
		wantScalingDepth int
	}{
		{
			name:             "literal bound does not scale",
			src:              "void f() { for (var i = 0; i < 10; i++) { g(i); } }",
			wantLoopDepth:    1,
			wantScalingDepth: 0,
		},
		{
			name:             "for-in scales with its collection",
			src:              "void f(items) { for (final x in items) { g(x); } }",
			wantLoopDepth:    1,
			wantScalingDepth: 1,
		},
		{
			name:             "a length bound scales even in C-style form",
			src:              "void f(xs) { for (var i = 0; i < xs.length; i++) { g(i); } }",
			wantLoopDepth:    1,
			wantScalingDepth: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			props := funcProps(t, tc.src)
			gotLoop, _ := props["loop_depth"].(int)
			gotScaling, _ := props["scaling_loop_depth"].(int)
			if gotLoop != tc.wantLoopDepth {
				t.Errorf("loop_depth = %d, want %d", gotLoop, tc.wantLoopDepth)
			}
			if gotScaling != tc.wantScalingDepth {
				t.Errorf("scaling_loop_depth = %d, want %d", gotScaling, tc.wantScalingDepth)
			}
		})
	}
}

// TestCallsResolveOnlyToCallables pins that a call edge never binds to a constant.
//
// Bare call targets resolve by unique short name, and Dart's short names collide across
// kinds. immich declares the enum constant `LogLevel.severe` and separately calls
// `log.severe(...)` on a logger; with every symbol kind in the index the constant was
// the unique `severe`, so 117 call sites bound to it and the god-class explainer
// reported a data constant as a high-fan-in symbol with 117 dependents.
func TestCallsResolveOnlyToCallables(t *testing.T) {
	src := `
enum LogLevel { info, severe }

class Logger {
  void warn(String m) {}
}

void doWork(Logger log) {
  log.severe('boom');
  log.warn('careful');
}
`
	all := resolveCallTargets(walkSource(t, "lib/a.dart", src))

	var work *facts.Fact
	for i := range all {
		if all[i].Name == "lib.doWork" {
			work = &all[i]
		}
	}
	if work == nil {
		t.Fatal("missing doWork")
	}
	for _, r := range work.Relations {
		if r.Kind != facts.RelCalls {
			continue
		}
		if r.Target == "lib.LogLevel.severe" {
			t.Error("a call resolved to an ENUM CONSTANT; it must stay bare instead")
		}
	}
	// The callable one still resolves, so the guard costs no real edge.
	found := false
	for _, r := range work.Relations {
		if r.Kind == facts.RelCalls && r.Target == "lib.Logger.warn" {
			found = true
		}
	}
	if !found {
		t.Error("a call to a real method should still resolve to it")
	}
}
