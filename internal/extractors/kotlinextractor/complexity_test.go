package kotlinextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func kIntProp(t *testing.T, f facts.Fact, key string) int {
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

func kStrSlice(f facts.Fact, key string) []string {
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

func kContains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

func TestKtComplexity_ForLoop(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  for (x in items) { use(x) }\n}", false)
	f, ok := findFact(ff, "pkg.r")
	if !ok {
		t.Fatalf("missing pkg.r; got %v", ff)
	}
	if got := kIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := kIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "pkg.use") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.use", cil)
	}
}

func TestKtComplexity_ForEachLambdaIsLoop(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  items.forEach { g(it) }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if got := kIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1 (forEach lambda is a loop)", got)
	}
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "pkg.g") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.g", cil)
	}
}

func TestKtComplexity_NestedLambdaIterators(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  outer.forEach { o -> o.items.map { inner(it) } }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if got := kIntProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2 (nested forEach/map)", got)
	}
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "pkg.inner") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.inner", cil)
	}
}

func TestKtComplexity_IteratorReceiverEvaluatedOnce(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  service.load().forEach { use(it) }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	cil := kStrSlice(f, "calls_in_loop")
	if !kContains(cil, "pkg.use") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.use", cil)
	}
	if kContains(cil, "service.load") {
		t.Errorf("calls_in_loop = %v, must NOT contain service.load (receiver runs once)", cil)
	}
}

func TestKtComplexity_NonIteratorLambdaNotLoop(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  runBlocking { persist() }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("runBlocking { } must not be a loop; loop_depth=%v", f.Props["loop_depth"])
	}
	if cil := kStrSlice(f, "calls_in_loop"); kContains(cil, "pkg.persist") {
		t.Errorf("calls_in_loop = %v, must NOT contain pkg.persist (lambda runs once)", cil)
	}
}

func TestKtComplexity_InLoopMethodCallCaptured(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  items.forEach { dao.insert(it) }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "dao.insert") {
		t.Errorf("calls_in_loop = %v, want to contain dao.insert", cil)
	}
}

func TestKtComplexity_RecursiveSelf(t *testing.T) {
	ff := extractAST(t, "fun fib(n: Int): Int {\n  if (n < 2) return n\n  return fib(n - 1) + fib(n - 2)\n}", false)
	f, _ := findFact(ff, "pkg.fib")
	v, ok := f.Props["recursive_self"].(bool)
	if !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v), want true", f.Props["recursive_self"], ok)
	}
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("loop_depth should be omitted for a loop-free function, got %v", f.Props["loop_depth"])
	}
}

func TestKtComplexity_WhileLoop(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  while (ready()) { step() }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if got := kIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "pkg.step") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.step", cil)
	}
}
