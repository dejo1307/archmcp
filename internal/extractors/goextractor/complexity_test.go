package goextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// --- helpers ---

func intProp(t *testing.T, f facts.Fact, key string) int {
	t.Helper()
	v, ok := f.Props[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64: // after a JSONL round-trip ints decode as float64
		return int(n)
	}
	t.Fatalf("prop %q is not numeric: %T", key, v)
	return 0
}

func strSliceProp(f facts.Fact, key string) []string {
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

func containsStr(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

// --- tests ---

func TestExtract_LoopMetrics_NestedLoops(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/proc.go": `package pkg

func Process(items []int) {
	for _, x := range items {
		for y := 0; y < x; y++ {
			helper()
		}
	}
}

func helper() {}
`,
	})

	f, ok := findFact(ff, "pkg.Process")
	if !ok {
		t.Fatalf("missing pkg.Process; got %v", ff)
	}
	if got := intProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2", got)
	}
	if got := intProp(t, f, "loop_count"); got != 2 {
		t.Errorf("loop_count = %d, want 2", got)
	}
	// 1 (base) + range (1) + for (1) = 3
	if got := intProp(t, f, "cyclomatic"); got != 3 {
		t.Errorf("cyclomatic = %d, want 3", got)
	}
	if cil := strSliceProp(f, "calls_in_loop"); !containsStr(cil, "pkg.helper") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.helper", cil)
	}
}

func TestExtract_CallsInLoop_InVsOutside(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/mixed.go": `package pkg

func Mixed(items []int) {
	setup()
	for _, x := range items {
		inLoop(x)
	}
}

func setup()       {}
func inLoop(x int) {}
`,
	})

	f, ok := findFact(ff, "pkg.Mixed")
	if !ok {
		t.Fatalf("missing pkg.Mixed; got %v", ff)
	}
	// Both calls remain as call edges.
	if !hasRelation(f, facts.RelCalls, "pkg.setup") || !hasRelation(f, facts.RelCalls, "pkg.inLoop") {
		t.Errorf("expected call edges to both pkg.setup and pkg.inLoop; relations=%v", f.Relations)
	}
	cil := strSliceProp(f, "calls_in_loop")
	if !containsStr(cil, "pkg.inLoop") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.inLoop", cil)
	}
	if containsStr(cil, "pkg.setup") {
		t.Errorf("calls_in_loop = %v, must NOT contain pkg.setup (called outside loop)", cil)
	}
}

func TestExtract_RecursiveSelf(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/fib.go": `package pkg

func Fib(n int) int {
	if n < 2 {
		return n
	}
	return Fib(n-1) + Fib(n-2)
}
`,
	})

	f, ok := findFact(ff, "pkg.Fib")
	if !ok {
		t.Fatalf("missing pkg.Fib; got %v", ff)
	}
	v, ok := f.Props["recursive_self"].(bool)
	if !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v), want true", f.Props["recursive_self"], ok)
	}
	// if (1) contributes to cyclomatic; no loops.
	if got := intProp(t, f, "cyclomatic"); got != 2 {
		t.Errorf("cyclomatic = %d, want 2", got)
	}
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("loop_depth should be omitted for a loop-free function, got %v", f.Props["loop_depth"])
	}
}

func TestExtract_NoLoops_BaselineCyclomatic(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/simple.go": `package pkg

func Simple(a, b bool) bool {
	if a && b {
		return true
	}
	return false
}
`,
	})

	f, ok := findFact(ff, "pkg.Simple")
	if !ok {
		t.Fatalf("missing pkg.Simple; got %v", ff)
	}
	// 1 (base) + if (1) + && (1) = 3
	if got := intProp(t, f, "cyclomatic"); got != 3 {
		t.Errorf("cyclomatic = %d, want 3", got)
	}
	if _, present := f.Props["calls_in_loop"]; present {
		t.Errorf("calls_in_loop should be omitted, got %v", f.Props["calls_in_loop"])
	}
}
