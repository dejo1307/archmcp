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
