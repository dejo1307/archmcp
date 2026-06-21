package swiftextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func swIntProp(t *testing.T, f facts.Fact, key string) int {
	t.Helper()
	v, ok := f.Props[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	t.Fatalf("prop %q is not numeric: %T", key, v)
	return 0
}

func swStrSlice(f facts.Fact, key string) []string {
	v, ok := f.Props[key]
	if !ok {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, a := range s {
			if str, ok := a.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func swContains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

func TestSwComplexity_ForLoop(t *testing.T) {
	ff := extractAST(t, "func run() {\n  for x in items { use(x) }\n}", false)
	f, ok := findFact(ff, "pkg.run")
	if !ok {
		t.Fatalf("missing pkg.run; got %v", ff)
	}
	if got := swIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := swIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if cil := swStrSlice(f, "calls_in_loop"); !swContains(cil, "pkg.use") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.use", cil)
	}
}

func TestSwComplexity_ForEachClosureIsLoop(t *testing.T) {
	ff := extractAST(t, "func run() {\n  items.forEach { g($0) }\n}", false)
	f, _ := findFact(ff, "pkg.run")
	if got := swIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1 (forEach closure is a loop)", got)
	}
	if cil := swStrSlice(f, "calls_in_loop"); !swContains(cil, "pkg.g") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.g", cil)
	}
}

func TestSwComplexity_NestedClosureIterators(t *testing.T) {
	ff := extractAST(t, "func run() {\n  outer.forEach { o in\n    o.items.map { inner($0) }\n  }\n}", false)
	f, _ := findFact(ff, "pkg.run")
	if got := swIntProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2 (nested forEach/map)", got)
	}
	if cil := swStrSlice(f, "calls_in_loop"); !swContains(cil, "pkg.inner") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.inner", cil)
	}
}

func TestSwComplexity_IteratorReceiverEvaluatedOnce(t *testing.T) {
	// service.load() is the iterator receiver — evaluated once, not per element.
	ff := extractAST(t, "func run() {\n  service.load().forEach { use($0) }\n}", false)
	f, _ := findFact(ff, "pkg.run")
	cil := swStrSlice(f, "calls_in_loop")
	if !swContains(cil, "pkg.use") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.use", cil)
	}
	if swContains(cil, "service.load") {
		t.Errorf("calls_in_loop = %v, must NOT contain service.load (receiver runs once)", cil)
	}
}

func TestSwComplexity_NonIteratorClosureNotLoop(t *testing.T) {
	ff := extractAST(t, "func run() {\n  Task { persist() }\n}", false)
	f, _ := findFact(ff, "pkg.run")
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("Task { } closure must not be a loop; loop_depth=%v", f.Props["loop_depth"])
	}
	if cil := swStrSlice(f, "calls_in_loop"); swContains(cil, "pkg.persist") {
		t.Errorf("calls_in_loop = %v, must NOT contain pkg.persist (closure runs once)", cil)
	}
}

func TestSwComplexity_InLoopMethodCallCaptured(t *testing.T) {
	// A method call on a lowercase receiver inside a loop is captured (metrics-only)
	// so the enterprise keyword heuristic can flag per-iteration I/O.
	ff := extractAST(t, "func run() {\n  items.forEach { item in\n    context.fetch(item)\n  }\n}", false)
	f, _ := findFact(ff, "pkg.run")
	if cil := swStrSlice(f, "calls_in_loop"); !swContains(cil, "context.fetch") {
		t.Errorf("calls_in_loop = %v, want to contain context.fetch", cil)
	}
}

func TestSwComplexity_RecursiveSelf(t *testing.T) {
	ff := extractAST(t, "func fib(_ n: Int) -> Int {\n  if n < 2 { return n }\n  return fib(n - 1) + fib(n - 2)\n}", false)
	f, _ := findFact(ff, "pkg.fib")
	v, ok := f.Props["recursive_self"].(bool)
	if !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v), want true", f.Props["recursive_self"], ok)
	}
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("loop_depth should be omitted for a loop-free function, got %v", f.Props["loop_depth"])
	}
}

func TestSwComplexity_WhileLoop(t *testing.T) {
	ff := extractAST(t, "func run() {\n  while ready() { step() }\n}", false)
	f, _ := findFact(ff, "pkg.run")
	if got := swIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if cil := swStrSlice(f, "calls_in_loop"); !swContains(cil, "pkg.step") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.step", cil)
	}
}
