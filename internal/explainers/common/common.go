// Package common holds helpers shared by multiple explainers: module-level
// dependency-graph construction and statistical-outlier detection. Extracting
// these here keeps the per-explainer packages small and avoids the
// copy-pasted graph/path logic that previously lived in cycles and layers.
package common

import (
	"math"
	"sort"
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
//
// Test-role modules (test bundles / spec trees, tagged module_role=test) are
// excluded as both nodes and edge endpoints: they are not part of the production
// architecture, and a test target normally imports the very module it exercises,
// which would otherwise drag test bundles into cycle, layer-violation, and
// depth findings (the classic "the cycle chain mixes Tests/ and Sources/"
// artifact). This mirrors package-metrics, which already filters non-production
// roles. Modules with an absent or non-test role are kept (consumers treat an
// absent role as included).
func BuildModuleGraph(store *facts.Store) map[string][]string {
	graph := make(map[string][]string)

	modules := store.ByKind(facts.KindModule)
	moduleNames := make(map[string]bool)
	testModules := make(map[string]bool)
	for _, m := range modules {
		if role, _ := m.Props[facts.PropModuleRole].(string); role == facts.ModuleRoleTest {
			testModules[m.Name] = true
			continue
		}
		moduleNames[m.Name] = true
		if _, ok := graph[m.Name]; !ok {
			graph[m.Name] = nil
		}
	}

	deps := store.ByKind(facts.KindDependency)
	for _, dep := range deps {
		sourceModule := FileDir(dep.File)
		if testModules[sourceModule] {
			continue // edge out of a test bundle — not production architecture
		}

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

// StronglyConnectedComponents returns the strongly-connected components of a
// directed graph (adjacency list: node -> successors) using Tarjan's algorithm.
//
// The result is deterministic regardless of Go's randomized map iteration: nodes
// are visited in sorted order with sorted neighbor lists, each component's members
// are sorted, and the component list is ordered by each component's smallest
// member. Every key of graph appears in exactly one component (a node that appears
// only as a neighbor is included as its own singleton). Shared by the cycles and
// dependency-depth explainers, which both need a cycle-safe view of the module
// graph — factoring it here keeps a single, tested implementation.
func StronglyConnectedComponents(graph map[string][]string) [][]string {
	nodes := make([]string, 0, len(graph))
	for v := range graph {
		nodes = append(nodes, v)
	}
	sort.Strings(nodes)

	var (
		index    int
		stack    []string
		onStack  = make(map[string]bool)
		indices  = make(map[string]int)
		lowlinks = make(map[string]int)
		sccs     [][]string
	)

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indices[v] = index
		lowlinks[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		neighbors := append([]string(nil), graph[v]...)
		sort.Strings(neighbors)
		for _, w := range neighbors {
			if _, visited := indices[w]; !visited {
				strongConnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}

		if lowlinks[v] == indices[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Strings(scc)
			sccs = append(sccs, scc)
		}
	}

	for _, v := range nodes {
		if _, visited := indices[v]; !visited {
			strongConnect(v)
		}
	}

	sort.Slice(sccs, func(i, j int) bool { return sccs[i][0] < sccs[j][0] })
	return sccs
}
