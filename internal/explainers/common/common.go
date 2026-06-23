// Package common holds helpers shared by multiple explainers: module-level
// dependency-graph construction and statistical-outlier detection. Extracting
// these here keeps the per-explainer packages small and avoids the
// copy-pasted graph/path logic that previously lived in cycles and layers.
package common

import (
	"math"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// FileDir returns the directory portion of a file path, which enola uses as the
// canonical module name. A path with no separator maps to ".".
func FileDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

// IsExternalImport reports whether an import target points outside the repo
// (Go stdlib, third-party, or an npm package) rather than at an internal module.
func IsExternalImport(path string) bool {
	// Go external imports contain dots (fmt, net/http, github.com/...).
	// TS external imports don't start with . or / and aren't relative.
	if strings.HasPrefix(path, ".") || strings.HasPrefix(path, "/") {
		return false
	}
	// Go standard library or third-party.
	if strings.Contains(path, ".") || !strings.Contains(path, "/") {
		// Likely a Go stdlib or npm package (e.g., "fmt", "react", "@types/node").
		return true
	}
	return false
}

// ResolveRelativeImport resolves a "./x" or "../x" import target against the
// source module's path, yielding an absolute (repo-relative) module path.
func ResolveRelativeImport(sourceModule, target string) string {
	if !strings.HasPrefix(target, ".") {
		return target
	}

	parts := strings.Split(sourceModule, "/")
	targetParts := strings.Split(target, "/")

	for _, tp := range targetParts {
		switch tp {
		case ".":
			continue
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, tp)
		}
	}

	return strings.Join(parts, "/")
}

// BuildModuleGraph extracts the module-level import adjacency list from the
// store: module name -> list of internal modules it imports. External imports
// are dropped and relative imports are normalized. Every declared module is
// present as a key (with a possibly-empty edge list).
func BuildModuleGraph(store *facts.Store) map[string][]string {
	graph := make(map[string][]string)

	modules := store.ByKind(facts.KindModule)
	moduleNames := make(map[string]bool)
	for _, m := range modules {
		moduleNames[m.Name] = true
		if _, ok := graph[m.Name]; !ok {
			graph[m.Name] = nil
		}
	}

	deps := store.ByKind(facts.KindDependency)
	for _, dep := range deps {
		sourceModule := FileDir(dep.File)

		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			target := rel.Target

			if IsExternalImport(target) {
				continue
			}

			if strings.HasPrefix(target, ".") {
				target = ResolveRelativeImport(sourceModule, target)
			}

			if moduleNames[target] {
				graph[sourceModule] = append(graph[sourceModule], target)
			}
		}
	}

	return graph
}

// SymbolModule returns the module a symbol belongs to. Symbol names encode the
// module as the prefix before the first ".", e.g. "internal/auth.Login.Verify"
// -> "internal/auth". Names without a "." are returned unchanged.
func SymbolModule(name string) string {
	if i := strings.Index(name, "."); i >= 0 {
		return name[:i]
	}
	return name
}

// MeanStdDev returns the arithmetic mean and population standard deviation of
// the given values. Both are 0 for an empty slice.
func MeanStdDev(values []float64) (mean, std float64) {
	n := len(values)
	if n == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(n)

	var variance float64
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	variance /= float64(n)
	return mean, math.Sqrt(variance)
}

// OutlierThreshold returns mean + k*stddev for the given values — the cutoff
// above which a value is treated as a high outlier. Returns 0 for empty input.
func OutlierThreshold(values []float64, k float64) float64 {
	mean, std := MeanStdDev(values)
	return mean + k*std
}
