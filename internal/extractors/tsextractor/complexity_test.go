package tsextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func tsIntProp(t *testing.T, f facts.Fact, key string) int {
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

func tsStrSlice(f facts.Fact, key string) []string {
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

func tsContains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

func tsExtractFunc(t *testing.T, body, factName string) facts.Fact {
	t.Helper()
	ff := extractAll(t, map[string]string{"src/x.ts": body}, false)
	f, ok := findFact(ff, factName)
	if !ok {
		t.Fatalf("missing fact %q; got %v", factName, ff)
	}
	return f
}

func TestTsComplexity_ForOfLoop(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  for (const x of items) { use(x) }\n}", "src.r")
	if got := tsIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := tsIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if cil := tsStrSlice(f, "calls_in_loop"); !tsContains(cil, "src.use") {
		t.Errorf("calls_in_loop = %v, want to contain src.use", cil)
	}
}

func TestTsComplexity_ForEachCallbackIsLoop(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  items.forEach(x => g(x))\n}", "src.r")
	if got := tsIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1 (forEach callback is a loop)", got)
	}
	if cil := tsStrSlice(f, "calls_in_loop"); !tsContains(cil, "src.g") {
		t.Errorf("calls_in_loop = %v, want to contain src.g", cil)
	}
}

func TestTsComplexity_NestedArrayMethods(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  outer.forEach(o => o.items.map(x => inner(x)))\n}", "src.r")
	if got := tsIntProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2 (nested forEach/map)", got)
	}
	if cil := tsStrSlice(f, "calls_in_loop"); !tsContains(cil, "src.inner") {
		t.Errorf("calls_in_loop = %v, want to contain src.inner", cil)
	}
}

func TestTsComplexity_IteratorReceiverEvaluatedOnce(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  service.load().forEach(x => use(x))\n}", "src.r")
	cil := tsStrSlice(f, "calls_in_loop")
	if !tsContains(cil, "src.use") {
		t.Errorf("calls_in_loop = %v, want to contain src.use", cil)
	}
	if tsContains(cil, "service.load") {
		t.Errorf("calls_in_loop = %v, must NOT contain service.load (receiver runs once)", cil)
	}
}

func TestTsComplexity_NonIteratorCallbackNotLoop(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  setTimeout(() => persist(), 100)\n}", "src.r")
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("setTimeout callback must not be a loop; loop_depth=%v", f.Props["loop_depth"])
	}
	if cil := tsStrSlice(f, "calls_in_loop"); tsContains(cil, "src.persist") {
		t.Errorf("calls_in_loop = %v, must NOT contain src.persist (callback runs once)", cil)
	}
}

func TestTsComplexity_InLoopMethodCallCaptured(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  items.forEach(x => repo.findMany(x))\n}", "src.r")
	if cil := tsStrSlice(f, "calls_in_loop"); !tsContains(cil, "repo.findMany") {
		t.Errorf("calls_in_loop = %v, want to contain repo.findMany", cil)
	}
}

func TestTsComplexity_RecursiveFunction(t *testing.T) {
	f := tsExtractFunc(t, "export function fib(n: number): number {\n  if (n < 2) return n\n  return fib(n - 1) + fib(n - 2)\n}", "src.fib")
	if v, ok := f.Props["recursive_self"].(bool); !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v), want true", f.Props["recursive_self"], ok)
	}
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("loop_depth should be omitted for a loop-free function, got %v", f.Props["loop_depth"])
	}
}

func TestTsComplexity_RecursiveArrowConst(t *testing.T) {
	f := tsExtractFunc(t, "export const walk = (n: number): number => {\n  if (n <= 0) return 0\n  return walk(n - 1)\n}", "src.walk")
	if v, ok := f.Props["recursive_self"].(bool); !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v), want true (arrow const recursion)", f.Props["recursive_self"], ok)
	}
}

func TestTsComplexity_ReactEventHandlerInMapNotInLoop(t *testing.T) {
	// The classic React false-positive: an event handler defined inside a `.map`
	// render callback runs on click, NOT per render-iteration. It must NOT be
	// recorded as an in-loop call, while a function genuinely called in the
	// callback body is.
	src := `export function Table({ items }: { items: any[] }) {
  return items.map((x) => render(x, <button onClick={() => handleDelete(x.id)}>X</button>))
}`
	ff := extractAll(t, map[string]string{"src/Table.tsx": src}, false)
	f, ok := findFact(ff, "src.Table")
	if !ok {
		t.Fatalf("missing src.Table; got %v", ff)
	}
	cil := tsStrSlice(f, "calls_in_loop")
	if !tsContains(cil, "src.render") {
		t.Errorf("calls_in_loop = %v, want to contain src.render (called per element)", cil)
	}
	if tsContains(cil, "src.handleDelete") {
		t.Errorf("calls_in_loop = %v, must NOT contain src.handleDelete (deferred event handler)", cil)
	}
}

func TestTsComplexity_WhileLoop(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  while (ready()) { step() }\n}", "src.r")
	if got := tsIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if cil := tsStrSlice(f, "calls_in_loop"); !tsContains(cil, "src.step") {
		t.Errorf("calls_in_loop = %v, want to contain src.step", cil)
	}
}

func TestTsComplexity_CallsInScalingLoop_BoundedExcluded(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  for (const c of [1, 2]) { setup(c) }\n  for (const x of items) { consume(x) }\n}", "src.r")
	inLoop := tsStrSlice(f, "calls_in_loop")
	if !tsContains(inLoop, "src.setup") || !tsContains(inLoop, "src.consume") {
		t.Errorf("calls_in_loop = %v, want both setup and consume", inLoop)
	}
	scaling := tsStrSlice(f, "calls_in_scaling_loop")
	if !tsContains(scaling, "src.consume") {
		t.Errorf("calls_in_scaling_loop = %v, want consume (unbounded for..of)", scaling)
	}
	if tsContains(scaling, "src.setup") {
		t.Errorf("calls_in_scaling_loop = %v, must NOT contain setup (for..of over array literal)", scaling)
	}
}

// --- v99: calls_in_scaling_loop counts REPEATED loops, not just scaling ones --------
//
// `while (true)` adds no factor of n (it exits by break/return), but its body still runs
// many times — a reconnect loop, a retry, or a parent-chain walk doing one query per
// level. Its calls must remain N+1 candidates even though its depth is discounted.
func TestTsComplexity_CallsInScalingLoop_InfiniteLoopCallsRetained(t *testing.T) {
	for _, src := range []string{
		"export function r() {\n  while (true) { getById(id) }\n}",
		"export function r() {\n  do { getById(id) } while (true)\n}",
	} {
		f := tsExtractFunc(t, src, "src.r")
		if got := tsIntProp(t, f, "scaling_loop_depth"); got != 0 {
			t.Errorf("%s\n  scaling_loop_depth = %d, want 0", src, got)
		}
		scaling := tsStrSlice(f, "calls_in_scaling_loop")
		if !tsContains(scaling, "src.getById") {
			t.Errorf("%s\n  calls_in_scaling_loop = %v, want getById retained: an infinite "+
				"loop repeats, so a per-iteration query inside it is still an N+1 candidate", src, scaling)
		}
	}
}

// The key must be present (and empty) whenever calls_in_loop is, or perf's
// scalingLoopCalls() falls back to the unfiltered calls_in_loop.
func TestTsComplexity_CallsInScalingLoop_PresentButEmptyWhenAllBounded(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  for (const c of [1, 2]) { setup(c) }\n}", "src.r")
	if !tsContains(tsStrSlice(f, "calls_in_loop"), "src.setup") {
		t.Fatalf("calls_in_loop = %v, want setup", f.Props["calls_in_loop"])
	}
	if _, present := f.Props["calls_in_scaling_loop"]; !present {
		t.Fatalf("calls_in_scaling_loop must be present even when empty")
	}
	if got := tsStrSlice(f, "calls_in_scaling_loop"); len(got) != 0 {
		t.Fatalf("calls_in_scaling_loop = %v, want empty", got)
	}
}

func TestTsComplexity_CallsInScalingLoop_AbsentWithoutLoopCalls(t *testing.T) {
	f := tsExtractFunc(t, "export function r() {\n  let n = 0\n  for (const x of items) { n++ }\n  return n\n}", "src.r")
	if _, present := f.Props["calls_in_scaling_loop"]; present {
		t.Fatalf("calls_in_scaling_loop must be absent when calls_in_loop is")
	}
}
