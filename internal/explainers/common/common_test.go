package common

import (
	"math"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestIsExternalImport(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"fmt", true},                // Go stdlib (no slash)
		{"react", true},              // npm package (no slash)
		{"github.com/foo/bar", true}, // Go third-party (has dot)
		{"./relative", false},        // relative import
		{"../parent", false},         // parent relative import
		{"/absolute/path", false},    // absolute path
		{"internal/pkg", false},      // internal module (has slash, no dot)
		{"src/components", false},    // internal path (has slash, no dot)
		// Known edge case: @types/node has slash but is npm-external.
		// Current implementation returns false (treats as internal) because
		// it has "/" and no ".". This documents the behavior.
		{"@types/node", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsExternalImport(tt.path); got != tt.want {
				t.Errorf("IsExternalImport(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestResolveRelativeImport(t *testing.T) {
	tests := []struct {
		source string
		target string
		want   string
	}{
		{"src/components", "./utils", "src/components/utils"},
		{"src/components", "../hooks", "src/hooks"},
		{"src/components", "../../lib", "lib"},
		{"src/deep/nested", "../../../top", "top"},
		{"src", "./foo", "src/foo"},
		// "." as source: the dot stays in the joined result
		{".", "./foo", "./foo"},
		// Going up past root produces empty path
		{"src", "../../above", "above"},
	}

	for _, tt := range tests {
		t.Run(tt.source+"→"+tt.target, func(t *testing.T) {
			got := ResolveRelativeImport(tt.source, tt.target)
			if got != tt.want {
				t.Errorf("ResolveRelativeImport(%q, %q) = %q, want %q",
					tt.source, tt.target, got, tt.want)
			}
		})
	}
}

func TestFileDir(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"internal/auth/login.go", "internal/auth"},
		{"main.go", "."},
		{"a/b/c/d.ts", "a/b/c"},
	}
	for _, tt := range tests {
		if got := FileDir(tt.file); got != tt.want {
			t.Errorf("FileDir(%q) = %q, want %q", tt.file, got, tt.want)
		}
	}
}

func TestBuildModuleGraph(t *testing.T) {
	s := facts.NewStore()
	for _, m := range []string{"src/a", "src/b", "src/c"} {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	deps := map[string][]string{
		"src/a": {"src/b", "fmt", "github.com/foo/bar"},
		"src/b": {"src/c", "react"},
	}
	for src, targets := range deps {
		for _, tgt := range targets {
			s.Add(facts.Fact{
				Kind:      facts.KindDependency,
				File:      src + "/file.go",
				Relations: []facts.Relation{{Kind: facts.RelImports, Target: tgt}},
			})
		}
	}

	graph := BuildModuleGraph(s)

	// src/a should only have edge to src/b (fmt and github.com/foo/bar are external)
	if edges := graph["src/a"]; len(edges) != 1 || edges[0] != "src/b" {
		t.Errorf("src/a edges = %v, want [src/b]", edges)
	}
	// src/b should only have edge to src/c (react is external)
	if edges := graph["src/b"]; len(edges) != 1 || edges[0] != "src/c" {
		t.Errorf("src/b edges = %v, want [src/c]", edges)
	}
	// every declared module is a key, even with no outgoing edges
	if _, ok := graph["src/c"]; !ok {
		t.Error("src/c missing as a graph key")
	}
}

func TestSymbolModule(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"internal/auth.Login.Verify", "internal/auth"},
		{"pkg/foo.Bar", "pkg/foo"},
		{"standalone", "standalone"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := SymbolModule(tt.name); got != tt.want {
			t.Errorf("SymbolModule(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestMeanStdDev(t *testing.T) {
	if m, s := MeanStdDev(nil); m != 0 || s != 0 {
		t.Errorf("MeanStdDev(nil) = (%v, %v), want (0, 0)", m, s)
	}
	// values 2,4,4,4,5,5,7,9 → mean 5, population stddev 2
	vals := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	mean, std := MeanStdDev(vals)
	if math.Abs(mean-5) > 1e-9 {
		t.Errorf("mean = %v, want 5", mean)
	}
	if math.Abs(std-2) > 1e-9 {
		t.Errorf("stddev = %v, want 2", std)
	}
	if got := OutlierThreshold(vals, 2); math.Abs(got-9) > 1e-9 {
		t.Errorf("OutlierThreshold(vals, 2) = %v, want 9", got)
	}
}

// TestOutlierThreshold_ZeroStdDev: with identical values the std dev is 0, so the
// threshold equals the mean — every value is <= it, so nothing is a strict
// outlier (why an all-equal fan-in/complexity distribution flags nothing).
func TestOutlierThreshold_ZeroStdDev(t *testing.T) {
	vals := []float64{7, 7, 7, 7}
	mean, std := MeanStdDev(vals)
	if std != 0 {
		t.Errorf("std of identical values = %v, want 0", std)
	}
	if got := OutlierThreshold(vals, 2); got != mean {
		t.Errorf("OutlierThreshold(identical, 2) = %v, want mean %v", got, mean)
	}
}

// sccKey renders a component partition as a stable string for comparison.
func sccKey(sccs [][]string) string {
	parts := make([]string, len(sccs))
	for i, scc := range sccs {
		parts[i] = strings.Join(scc, ",")
	}
	return strings.Join(parts, " | ")
}

func TestStronglyConnectedComponents(t *testing.T) {
	tests := []struct {
		name  string
		graph map[string][]string
		want  string // sccKey of the expected (sorted) partition
	}{
		{"empty", map[string][]string{}, ""},
		{"single isolated", map[string][]string{"a": nil}, "a"},
		{"simple cycle", map[string][]string{"b": {"a"}, "a": {"b"}}, "a,b"},
		{"triangle + tail", map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a", "d"}, "d": nil}, "a,b,c | d"},
		{"two disjoint cycles", map[string][]string{"a": {"b"}, "b": {"a"}, "c": {"d"}, "d": {"c"}}, "a,b | c,d"},
		{"self loop is singleton", map[string][]string{"a": {"a"}}, "a"},
		{"neighbor-only node included", map[string][]string{"a": {"b"}}, "a | b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sccKey(StronglyConnectedComponents(tt.graph)); got != tt.want {
				t.Errorf("SCC(%v) = %q, want %q", tt.graph, got, tt.want)
			}
		})
	}
}

// TestStronglyConnectedComponents_Deterministic: repeated calls re-range the
// graph map (Go randomizes iteration), but the sorted output must be identical.
func TestStronglyConnectedComponents_Deterministic(t *testing.T) {
	graph := map[string][]string{
		"a": {"b"}, "b": {"c"}, "c": {"a", "d"},
		"d": {"e"}, "e": {"d"},
		"f": {"a"}, "g": nil,
	}
	want := sccKey(StronglyConnectedComponents(graph))
	for i := 0; i < 50; i++ {
		if got := sccKey(StronglyConnectedComponents(graph)); got != want {
			t.Fatalf("non-deterministic SCC output on iteration %d:\nwant %q\ngot  %q", i, want, got)
		}
	}
}

// TestBuildModuleGraph_ExcludesTestRole: modules tagged module_role=test are
// dropped as both nodes and edge endpoints.
func TestBuildModuleGraph_ExcludesTestRole(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindModule, Name: "src/app"})
	s.Add(facts.Fact{Kind: facts.KindModule, Name: "src/apptest",
		Props: map[string]any{facts.PropModuleRole: facts.ModuleRoleTest}})
	// app imports the test module and vice versa; the test edges must not appear.
	s.Add(facts.Fact{Kind: facts.KindDependency, File: "src/app/f.go",
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: "src/apptest"}}})
	s.Add(facts.Fact{Kind: facts.KindDependency, File: "src/apptest/f.go",
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: "src/app"}}})

	graph := BuildModuleGraph(s)
	if _, ok := graph["src/apptest"]; ok {
		t.Error("test-role module should not be a graph node")
	}
	if len(graph["src/app"]) != 0 {
		t.Errorf("edge to a test-role module should be dropped, got %v", graph["src/app"])
	}
}

// TestBuildModuleGraph_SingleSegmentInternalModule: a top-level internal module
// with a single-segment name (e.g. "config") must be included as an import edge.
// Before the fix, IsExternalImport("config") returned true and the edge was
// dropped before the authoritative moduleNames gate.
func TestBuildModuleGraph_SingleSegmentInternalModule(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindModule, Name: "config"})
	s.Add(facts.Fact{Kind: facts.KindModule, Name: "handlers"})
	s.Add(facts.Fact{
		Kind:      facts.KindDependency,
		File:      "handlers/h.go",
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: "config"}},
	})

	graph := BuildModuleGraph(s)

	if edges := graph["handlers"]; len(edges) != 1 || edges[0] != "config" {
		t.Errorf("handlers edges = %v, want [config]", edges)
	}
}

// TestBuildModuleGraph_ExternalStillDropped: single-segment names that are NOT
// declared modules (Go stdlib "fmt", npm "react") must still be dropped — the
// moduleNames gate remains authoritative after removing the IsExternalImport
// pre-filter.
func TestBuildModuleGraph_ExternalStillDropped(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindModule, Name: "handlers"})
	s.Add(facts.Fact{
		Kind: facts.KindDependency,
		File: "handlers/h.go",
		Relations: []facts.Relation{
			{Kind: facts.RelImports, Target: "fmt"},
			{Kind: facts.RelImports, Target: "react"},
		},
	})

	graph := BuildModuleGraph(s)

	if edges := graph["handlers"]; len(edges) != 0 {
		t.Errorf("handlers edges = %v, want none (fmt/react are not modules)", edges)
	}
}
