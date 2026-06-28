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
