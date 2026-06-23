package common

import (
	"math"
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
