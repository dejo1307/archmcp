package phpextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// cyclomaticOf extracts the cyclomatic value of the named symbol fact.
func cyclomaticOf(t *testing.T, result []facts.Fact, name string) int {
	t.Helper()
	f, ok := symbolsByName(result)[name]
	if !ok {
		t.Fatalf("missing symbol %q; got %v", name, keys(symbolsByName(result)))
	}
	c, ok := f.Props["cyclomatic"].(int)
	if !ok {
		t.Fatalf("symbol %q has no int cyclomatic prop: %v", name, f.Props["cyclomatic"])
	}
	return c
}

func TestComplexity_Cyclomatic(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"straight_line", `return 1;`, 1},
		{"single_if", `if ($x) { return 1; } return 0;`, 2},
		{"if_elseif", `if ($x) {} elseif ($y) {} else {}`, 3},
		{"logical_and", `if ($x && $y) {}`, 3},
		{"logical_or_coalesce", `$z = $a ?? $b; if ($x || $y) {}`, 4},
		{"foreach", `foreach ($items as $i) { echo $i; }`, 2},
		{"while", `while ($x) { $x--; }`, 2},
		{"ternary", `$r = $x ? 1 : 2;`, 2},
		{"switch", `switch ($x) { case 1: break; case 2: break; default: break; }`, 3},
		{"match", `$r = match($x) { 1 => "a", 2 => "b", default => "c" };`, 3},
		{"try_catch", `try { risky(); } catch (\Exception $e) {}`, 2},
		{"nested", `foreach ($a as $x) { if ($x > 0) { foo(); } }`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "<?php\nfunction f() {\n" + tc.body + "\n}\n"
			result := extractFileAST([]byte(src), "x.php")
			if got := cyclomaticOf(t, result, "f"); got != tc.want {
				t.Errorf("cyclomatic(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestComplexity_LoopMetricsAndRecursion(t *testing.T) {
	src := `<?php
function walk($nodes) {
    foreach ($nodes as $n) {
        foreach ($n->children as $c) {
            walk($c);
        }
    }
}
`
	result := extractFileAST([]byte(src), "x.php")
	f := symbolsByName(result)["walk"]

	if d, _ := f.Props["loop_depth"].(int); d != 2 {
		t.Errorf("loop_depth = %v, want 2", f.Props["loop_depth"])
	}
	if c, _ := f.Props["loop_count"].(int); c != 2 {
		t.Errorf("loop_count = %v, want 2", f.Props["loop_count"])
	}
	if f.Props["recursive_self"] != true {
		t.Errorf("walk should be flagged recursive_self; props %v", f.Props)
	}
	calls, _ := f.Props["calls_in_loop"].([]string)
	if len(calls) == 0 {
		t.Errorf("expected calls_in_loop to include walk; props %v", f.Props)
	}
}

// --- Bounded-loop discounting (GAP-PH-01, cacheVersion v106) -----------------
// PHP joins the Go/Python/TS/Kotlin/Java convention. A constant-count loop — a
// literal-bounded `for ($i = 0; $i < 3; $i++)` or a `foreach` over an array literal —
// is discounted from scaling_loop_depth; an infinite `while (true)` is discounted from
// the exponent but keeps its per-iteration calls as N+1 candidates.

func phpFn(t *testing.T, body string) facts.Fact {
	t.Helper()
	src := "<?php\nfunction f($xs, $n) {\n" + body + "\n}\n"
	f, ok := symbolsByName(extractFileAST([]byte(src), "x.php"))["f"]
	if !ok {
		t.Fatalf("missing symbol f")
	}
	return f
}

func phpIntProp(f facts.Fact, key string) int {
	if v, ok := f.Props[key].(int); ok {
		return v
	}
	return 0
}

func phpHasProp(f facts.Fact, key string) bool {
	_, ok := f.Props[key]
	return ok
}

func phpStrSlice(f facts.Fact, key string) []string {
	s, _ := f.Props[key].([]string)
	return s
}

func TestPhpComplexity_ScalingLoopDepth_ConstantForDiscounted(t *testing.T) {
	f := phpFn(t, "for ($i = 0; $i < 3; $i++) { work(); }")
	if got := phpIntProp(f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if !phpHasProp(f, "scaling_loop_depth") {
		t.Fatalf("scaling_loop_depth must be present (even 0)")
	}
	if got := phpIntProp(f, "scaling_loop_depth"); got != 0 {
		t.Errorf("scaling_loop_depth = %d, want 0 (literal-bounded for adds no factor of n)", got)
	}
}

func TestPhpComplexity_ScalingLoopDepth_VariableForNotDiscounted(t *testing.T) {
	f := phpFn(t, "for ($i = 0; $i < $n; $i++) { work(); }")
	if got := phpIntProp(f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1 (data-derived bound scales)", got)
	}
}

func TestPhpComplexity_ScalingLoopDepth_ForeachScales(t *testing.T) {
	f := phpFn(t, "foreach ($xs as $x) { work(); }")
	if got := phpIntProp(f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1 (foreach over a variable scales)", got)
	}
}

func TestPhpComplexity_ScalingLoopDepth_ConstantForeachDiscounted(t *testing.T) {
	f := phpFn(t, "foreach ([1, 2, 3] as $x) { work(); }")
	if got := phpIntProp(f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := phpIntProp(f, "scaling_loop_depth"); got != 0 {
		t.Errorf("scaling_loop_depth = %d, want 0 (an array literal iterates a fixed count)", got)
	}
}

func TestPhpComplexity_ScalingLoopDepth_ConstantOuterScalingInner(t *testing.T) {
	f := phpFn(t, "for ($i = 0; $i < 3; $i++) { foreach ($xs as $x) { work(); } }")
	if got := phpIntProp(f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2", got)
	}
	if got := phpIntProp(f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1 (only the inner scaling loop counts)", got)
	}
}

func TestPhpComplexity_ScalingLoopDepth_InfiniteWhileDiscounted(t *testing.T) {
	f := phpFn(t, "while (true) { work(); }")
	if got := phpIntProp(f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := phpIntProp(f, "scaling_loop_depth"); got != 0 {
		t.Errorf("scaling_loop_depth = %d, want 0 (while(true) adds no factor of n)", got)
	}
}

func TestPhpComplexity_ScalingLoopDepth_ConditionalWhileNotDiscounted(t *testing.T) {
	f := phpFn(t, "while ($n > 0) { $n--; work(); }")
	if got := phpIntProp(f, "scaling_loop_depth"); got != 1 {
		t.Errorf("scaling_loop_depth = %d, want 1 (a data-driven while scales)", got)
	}
}

func TestPhpComplexity_ScalingLoopDepth_AbsentWithoutLoops(t *testing.T) {
	f := phpFn(t, "work();")
	if phpHasProp(f, "scaling_loop_depth") {
		t.Errorf("scaling_loop_depth must be omitted for a loop-free function, got %v", f.Props["scaling_loop_depth"])
	}
}

func TestPhpComplexity_CallsInScalingLoop_ConstantExcluded(t *testing.T) {
	f := phpFn(t, "for ($i = 0; $i < 3; $i++) { db_query(); }")
	if len(phpStrSlice(f, "calls_in_loop")) == 0 {
		t.Errorf("calls_in_loop should include db_query; props %v", f.Props)
	}
	if !phpHasProp(f, "calls_in_scaling_loop") {
		t.Fatalf("calls_in_scaling_loop must be present (even empty) whenever calls_in_loop is")
	}
	if n := len(phpStrSlice(f, "calls_in_scaling_loop")); n != 0 {
		t.Errorf("calls_in_scaling_loop has %d entries, want 0 (call is inside a constant loop)", n)
	}
}

func TestPhpComplexity_CallsInScalingLoop_InfiniteLoopCallsRetained(t *testing.T) {
	f := phpFn(t, "while (true) { db_query(); }")
	if len(phpStrSlice(f, "calls_in_scaling_loop")) == 0 {
		t.Errorf("calls_in_scaling_loop should retain db_query (infinite loop still repeats); props %v", f.Props)
	}
}

func TestPhpComplexity_CallsInScalingLoop_ScalingRetained(t *testing.T) {
	f := phpFn(t, "foreach ($xs as $x) { db_query(); }")
	if len(phpStrSlice(f, "calls_in_scaling_loop")) == 0 {
		t.Errorf("calls_in_scaling_loop should retain db_query; props %v", f.Props)
	}
}

func TestPhpComplexity_CallsInScalingLoop_PresentButEmptyWhenAllBounded(t *testing.T) {
	f := phpFn(t, "for ($i = 0; $i < 3; $i++) { db_query(); }")
	if !phpHasProp(f, "calls_in_scaling_loop") {
		t.Fatalf("calls_in_scaling_loop must be present (even empty)")
	}
	if n := len(phpStrSlice(f, "calls_in_scaling_loop")); n != 0 {
		t.Errorf("calls_in_scaling_loop has %d entries, want 0", n)
	}
}
