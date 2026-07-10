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

func TestExtract_ScalingLoopDepth_BoundedDiscounted(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/proc.go": `package pkg

func Process(items []int) {
	for _, x := range items {
		for _, m := range []string{"a", "b"} {
			use(x, m)
		}
	}
}

func Poll() {
	for {
		tick()
	}
}

func use(x int, m string) {}
func tick()               {}
`,
	})

	f, _ := findFact(ff, "pkg.Process")
	if got := intProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2", got)
	}
	// Inner range is over a composite literal → bounded → only the outer loop scales.
	if got := intProp(t, f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1", got)
	}

	p, _ := findFact(ff, "pkg.Poll")
	if got := intProp(t, p, "scaling_loop_depth"); got != 0 {
		t.Errorf("infinite for{} scaling_loop_depth = %d, want 0", got)
	}
}

func TestExtract_CallsInScalingLoop_BoundedExcluded(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/proc.go": `package pkg

func Process(items []int) {
	for _, c := range []string{"a", "b"} {
		setup(c)
	}
	for _, x := range items {
		consume(x)
	}
}

func setup(c string) {}
func consume(x int)   {}
`,
	})
	f, _ := findFact(ff, "pkg.Process")
	scaling := strSliceProp(f, "calls_in_scaling_loop")
	if !containsStr(scaling, "pkg.consume") {
		t.Errorf("calls_in_scaling_loop = %v, want consume (range over slice arg)", scaling)
	}
	if containsStr(scaling, "pkg.setup") {
		t.Errorf("calls_in_scaling_loop = %v, must NOT contain setup (range over composite literal)", scaling)
	}
}

// --- v99: calls_in_scaling_loop counts REPEATED loops, not just scaling ones --------
//
// A bare `for {}` adds no factor of n (it is exited by break/return), but its body still
// runs many times — a parent-chain walk doing one query per level. Its calls must stay
// N+1 candidates even though its depth is discounted. Reproduced on fairwayhub/golf:
// OrganizationRepository.GetOrganizationPath (`for { org = GetByID(parentID) }`) and
// GroupRepository.makeUniqueSlug (`for { QueryRowContext(...) }`) are both real
// high/medium-severity N+1 findings that ride on this.
func TestExtract_CallsInScalingLoop_InfiniteLoopCallsRetained(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/proc.go": `package pkg

func GetPath(id int) {
	for {
		getByID(id)
	}
}

func getByID(id int) {}
`,
	})
	f, _ := findFact(ff, "pkg.GetPath")
	if got := intProp(t, f, "scaling_loop_depth"); got != 0 {
		t.Errorf("scaling_loop_depth = %d, want 0 (an infinite loop adds no factor of n)", got)
	}
	scaling := strSliceProp(f, "calls_in_scaling_loop")
	if !containsStr(scaling, "pkg.getByID") {
		t.Errorf("calls_in_scaling_loop = %v, want getByID retained: an infinite loop "+
			"repeats, so a per-iteration query inside it is still an N+1 candidate", scaling)
	}
}

// calls_in_scaling_loop must be PRESENT (and empty) whenever calls_in_loop is, or
// perf.scalingLoopCalls() reads its absence as "extractor never computed the subset"
// and falls back to the unfiltered calls_in_loop — defeating the discount in exactly
// the case it exists for.
func TestExtract_CallsInScalingLoop_PresentButEmptyWhenAllBounded(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/proc.go": `package pkg

func Seed() {
	for _, c := range []string{"a", "b"} {
		setup(c)
	}
}

func setup(c string) {}
`,
	})
	f, _ := findFact(ff, "pkg.Seed")
	if !containsStr(strSliceProp(f, "calls_in_loop"), "pkg.setup") {
		t.Fatalf("calls_in_loop = %v, want setup", f.Props["calls_in_loop"])
	}
	v, present := f.Props["calls_in_scaling_loop"]
	if !present {
		t.Fatalf("calls_in_scaling_loop must be present even when empty")
	}
	if got := strSliceProp(f, "calls_in_scaling_loop"); len(got) != 0 {
		t.Fatalf("calls_in_scaling_loop = %v (%T), want empty", got, v)
	}
}

// ...and absent when there are no in-loop calls at all.
func TestExtract_CallsInScalingLoop_AbsentWithoutLoopCalls(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"pkg/proc.go": `package pkg

func Count(items []int) int {
	n := 0
	for range items {
		n++
	}
	return n
}
`,
	})
	f, _ := findFact(ff, "pkg.Count")
	if _, present := f.Props["calls_in_scaling_loop"]; present {
		t.Fatalf("calls_in_scaling_loop must be absent when calls_in_loop is")
	}
}
