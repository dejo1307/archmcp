package javaextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func jIntProp(t *testing.T, f facts.Fact, key string) int {
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

func jStrSlice(f facts.Fact, key string) []string {
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

func jContains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

// javaClass wraps a class body in `package m; public class C { ... }` and returns
// the fact named "m.C.<factMethod>".
func javaClass(t *testing.T, body, factMethod string) facts.Fact {
	t.Helper()
	src := "package m;\npublic class C {\n" + body + "\n}\n"
	ff := extractAll(t, map[string]string{"m/C.java": src})
	f, ok := findFact(ff, "m.C."+factMethod)
	if !ok {
		t.Fatalf("missing m.C.%s; got %v", factMethod, names(ff))
	}
	return f
}

func TestJavaComplexity_EnhancedForLoop(t *testing.T) {
	f := javaClass(t, "void run(java.util.List<Item> items) {\n  for (Item x : items) { process(x); }\n}\nvoid process(Item x) {}", "run")
	if got := jIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := jIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if cil := jStrSlice(f, "calls_in_loop"); !jContains(cil, "m.C.process") {
		t.Errorf("calls_in_loop = %v, want to contain m.C.process", cil)
	}
}

func TestJavaComplexity_NestedLoops(t *testing.T) {
	f := javaClass(t, "void run() {\n  for (int i=0;i<n;i++) { for (int j=0;j<m;j++) { tick(); } }\n}\nvoid tick() {}", "run")
	if got := jIntProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2", got)
	}
	if cil := jStrSlice(f, "calls_in_loop"); !jContains(cil, "m.C.tick") {
		t.Errorf("calls_in_loop = %v, want to contain m.C.tick", cil)
	}
}

func TestJavaComplexity_WhileLoop(t *testing.T) {
	f := javaClass(t, "void run() {\n  while (cond()) { step(); }\n}\nboolean cond() { return true; }\nvoid step() {}", "run")
	if got := jIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if cil := jStrSlice(f, "calls_in_loop"); !jContains(cil, "m.C.step") {
		t.Errorf("calls_in_loop = %v, want to contain m.C.step", cil)
	}
}

func TestJavaComplexity_StreamForEachIsLoop(t *testing.T) {
	f := javaClass(t, "void run(java.util.List<Item> items) {\n  items.forEach(x -> handle(x));\n}\nvoid handle(Item x) {}", "run")
	if got := jIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1 (stream forEach is a loop)", got)
	}
	if cil := jStrSlice(f, "calls_in_loop"); !jContains(cil, "m.C.handle") {
		t.Errorf("calls_in_loop = %v, want to contain m.C.handle", cil)
	}
}

func TestJavaComplexity_InLoopRepoCallCaptured(t *testing.T) {
	f := javaClass(t, "void run(java.util.List<Long> ids) {\n  for (Long id : ids) { repo.findById(id); }\n}", "run")
	if cil := jStrSlice(f, "calls_in_loop"); !jContains(cil, "repo.findById") {
		t.Errorf("calls_in_loop = %v, want to contain repo.findById", cil)
	}
}

func TestJavaComplexity_NestedDeferredLambdaNotInLoop(t *testing.T) {
	f := javaClass(t,
		"void run(java.util.List<Item> items) {\n  items.forEach(x -> { use(x); schedule(() -> defer(x)); });\n}\nvoid use(Item x) {}\nvoid schedule(Runnable r) {}\nvoid defer(Item x) {}",
		"run")
	cil := jStrSlice(f, "calls_in_loop")
	if !jContains(cil, "m.C.use") {
		t.Errorf("calls_in_loop = %v, want to contain m.C.use (per element)", cil)
	}
	if jContains(cil, "m.C.defer") {
		t.Errorf("calls_in_loop = %v, must NOT contain m.C.defer (deferred Runnable lambda)", cil)
	}
}

func TestJavaComplexity_RecursiveSelf(t *testing.T) {
	f := javaClass(t, "int fib(int n) {\n  if (n < 2) return n;\n  return fib(n - 1) + fib(n - 2);\n}", "fib")
	if v, ok := f.Props["recursive_self"].(bool); !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v), want true", f.Props["recursive_self"], ok)
	}
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("loop_depth should be omitted for a loop-free method, got %v", f.Props["loop_depth"])
	}
}

func jHasProp(f facts.Fact, key string) bool {
	_, ok := f.Props[key]
	return ok
}

// --- Bounded-loop discounting (GAP-JV-01, cacheVersion v104) -----------------
// Java joins the Go/Python/TypeScript/Kotlin convention: loop_depth counts every
// loop, scaling_loop_depth counts only input-scaling loops (the Big-O exponent),
// and calls_in_scaling_loop is the N+1-candidate subset — calls inside a loop that
// REPEATS a non-constant number of times (a constant for(i<3) is excluded; an
// infinite while(true) is retained, because it still runs many times).

func TestJavaComplexity_ScalingLoopDepth_ConstantForDiscounted(t *testing.T) {
	f := javaClass(t, "void run() {\n  for (int i = 0; i < 3; i++) { work(); }\n}\nvoid work() {}", "run")
	if got := jIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if !jHasProp(f, "scaling_loop_depth") {
		t.Fatalf("scaling_loop_depth must be present (even when 0) so the consumer distinguishes 'all bounded' from 'signal absent'")
	}
	if got := jIntProp(t, f, "scaling_loop_depth"); got != 0 {
		t.Errorf("scaling_loop_depth = %d, want 0 (a literal-bounded for adds no factor of n)", got)
	}
}

func TestJavaComplexity_ScalingLoopDepth_VariableForNotDiscounted(t *testing.T) {
	f := javaClass(t, "void run(int n) {\n  for (int i = 0; i < n; i++) { work(); }\n}\nvoid work() {}", "run")
	if got := jIntProp(t, f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1 (a data-derived bound scales)", got)
	}
}

func TestJavaComplexity_ScalingLoopDepth_EnhancedForScales(t *testing.T) {
	f := javaClass(t, "void run(java.util.List<Item> items) {\n  for (Item x : items) { process(x); }\n}\nvoid process(Item x) {}", "run")
	if got := jIntProp(t, f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1 (iterating a variable collection scales)", got)
	}
}

func TestJavaComplexity_ScalingLoopDepth_ConstantIterableDiscounted(t *testing.T) {
	f := javaClass(t, "void run() {\n  for (Item x : List.of(a, b, c)) { process(x); }\n}\nvoid process(Item x) {}", "run")
	if got := jIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := jIntProp(t, f, "scaling_loop_depth"); got != 0 {
		t.Errorf("scaling_loop_depth = %d, want 0 (a collection literal iterates a fixed count)", got)
	}
}

func TestJavaComplexity_ScalingLoopDepth_ConstantOuterScalingInner(t *testing.T) {
	f := javaClass(t, "void run(java.util.List<Item> items) {\n  for (int i = 0; i < 3; i++) { for (Item x : items) { process(x); } }\n}\nvoid process(Item x) {}", "run")
	if got := jIntProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2", got)
	}
	if got := jIntProp(t, f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1 (only the inner scaling loop counts)", got)
	}
}

func TestJavaComplexity_ScalingLoopDepth_InfiniteWhileDiscounted(t *testing.T) {
	f := javaClass(t, "void run() {\n  while (true) { poll(); }\n}\nvoid poll() {}", "run")
	if got := jIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := jIntProp(t, f, "scaling_loop_depth"); got != 0 {
		t.Errorf("scaling_loop_depth = %d, want 0 (an infinite loop adds no factor of n)", got)
	}
}

func TestJavaComplexity_ScalingLoopDepth_ConditionalWhileNotDiscounted(t *testing.T) {
	f := javaClass(t, "void run() {\n  while (cond()) { step(); }\n}\nboolean cond() { return true; }\nvoid step() {}", "run")
	if got := jIntProp(t, f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1 (a data-driven while scales)", got)
	}
}

func TestJavaComplexity_ScalingLoopDepth_AbsentWithoutLoops(t *testing.T) {
	f := javaClass(t, "void run() {\n  work();\n}\nvoid work() {}", "run")
	if jHasProp(f, "scaling_loop_depth") {
		t.Errorf("scaling_loop_depth must be omitted for a loop-free method, got %v", f.Props["scaling_loop_depth"])
	}
}

func TestJavaComplexity_LoopCountAndCyclomaticUnchangedByBounding(t *testing.T) {
	// A constant loop still counts as a loop (cyclomatic + loop_count) — only the
	// Big-O exponent is discounted.
	f := javaClass(t, "void run() {\n  for (int i = 0; i < 3; i++) { work(); }\n}\nvoid work() {}", "run")
	if got := jIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if got := jIntProp(t, f, "cyclomatic"); got != 2 {
		t.Errorf("cyclomatic = %d, want 2 (1 + the loop decision point)", got)
	}
}

func TestJavaComplexity_CallsInScalingLoop_ConstantExcluded(t *testing.T) {
	f := javaClass(t, "void run() {\n  for (int i = 0; i < 3; i++) { repo.findById(i); }\n}", "run")
	if !jContains(jStrSlice(f, "calls_in_loop"), "repo.findById") {
		t.Errorf("calls_in_loop = %v, want to contain repo.findById", jStrSlice(f, "calls_in_loop"))
	}
	if !jHasProp(f, "calls_in_scaling_loop") {
		t.Fatalf("calls_in_scaling_loop must be present (even when empty) whenever calls_in_loop is")
	}
	if jContains(jStrSlice(f, "calls_in_scaling_loop"), "repo.findById") {
		t.Errorf("calls_in_scaling_loop = %v, must NOT contain a call made only inside a constant loop", jStrSlice(f, "calls_in_scaling_loop"))
	}
}

func TestJavaComplexity_CallsInScalingLoop_InfiniteLoopCallsRetained(t *testing.T) {
	f := javaClass(t, "void run() {\n  while (true) { repo.findById(id); }\n}", "run")
	if !jContains(jStrSlice(f, "calls_in_scaling_loop"), "repo.findById") {
		t.Errorf("calls_in_scaling_loop = %v, want to contain repo.findById (an infinite loop still repeats — N+1 candidate)", jStrSlice(f, "calls_in_scaling_loop"))
	}
}

func TestJavaComplexity_CallsInScalingLoop_ScalingRetained(t *testing.T) {
	f := javaClass(t, "void run(java.util.List<Long> ids) {\n  for (Long id : ids) { repo.findById(id); }\n}", "run")
	if !jContains(jStrSlice(f, "calls_in_scaling_loop"), "repo.findById") {
		t.Errorf("calls_in_scaling_loop = %v, want to contain repo.findById", jStrSlice(f, "calls_in_scaling_loop"))
	}
}

func TestJavaComplexity_CallsInScalingLoop_PresentButEmptyWhenAllBounded(t *testing.T) {
	f := javaClass(t, "void run() {\n  for (int i = 0; i < 3; i++) { work(); }\n}\nvoid work() {}", "run")
	if !jHasProp(f, "calls_in_scaling_loop") {
		t.Fatalf("calls_in_scaling_loop must be present (even empty) so the consumer does not fall back to the unfiltered calls_in_loop")
	}
	if n := len(jStrSlice(f, "calls_in_scaling_loop")); n != 0 {
		t.Errorf("calls_in_scaling_loop has %d entries, want 0 (every in-loop call sits inside a constant loop)", n)
	}
}

func TestJavaComplexity_CallsInScalingLoop_AbsentWithoutLoopCalls(t *testing.T) {
	f := javaClass(t, "void run() {\n  work();\n}\nvoid work() {}", "run")
	if jHasProp(f, "calls_in_scaling_loop") {
		t.Errorf("calls_in_scaling_loop must be omitted when there are no in-loop calls, got %v", f.Props["calls_in_scaling_loop"])
	}
}
