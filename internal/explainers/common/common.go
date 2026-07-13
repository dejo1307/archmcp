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

// OversizedClusterModules is the module-count above which a strongly-connected
// component is treated as a coupling *cluster* rather than a discrete, actionable
// tangle. In an autoloaded language (Ruby/Rails) mutual constant references across
// many directories are the expected topology, so a large SCC is a coupling-density
// signal, not a fixable cycle or a genuine deep layering. Shared by the cycles
// explainer (softens such SCCs into a cluster note) and the depth explainer (counts
// such an SCC as one logical layer instead of its full size).
const OversizedClusterModules = 8

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
	return BuildModuleGraphExcluding(store)
}

// isTestModule reports whether a module is test scaffolding rather than production
// architecture, and must therefore be kept out of the coupling graph the cycles and
// dependency-depth explainers read.
//
// The extractor's `module_role` prop is AUTHORITATIVE where it exists: it comes from
// a build file, not a guess — an SPM/Xcode target type, a Gradle source set. A module
// the extractor tagged `production` stays in the graph even if its path looks like a
// test tree, because the build system is right and the path is not. nan/nebenan-android-app
// ships a real package called `app/src/main/java/…/ui/base/testing`; Gradle says
// `src/main`, so it is production, and no path heuristic gets to overrule that.
//
// But only java, kotlin, ruby and swift emit the prop at all. Go, Python, TypeScript,
// PHP and C/C++ emit NONE — and the exclusion used to key on the prop alone, so their
// test trees walked straight into the graph. On python/superset that meant ALL TEN
// dependency-depth findings were test modules (`tests/unit_tests/dao (depth 11)`, …)
// and 2 of the 10 cycles ran through them: an explainer whose entire output was test
// scaffolding. Where the prop is absent, fall back to the path.
func isTestModule(m facts.Fact) bool {
	switch role, _ := m.Props[facts.PropModuleRole].(string); role {
	case facts.ModuleRoleTest:
		return true
	case facts.ModuleRoleProduction, facts.ModuleRoleTooling:
		return false // authoritative: the build file said so
	}
	// No usable signal (absent, "", or "unknown") — the five untagged languages.
	return facts.IsTestPath(m.Name)
}

// BuildModuleGraphExcluding is BuildModuleGraph with the ability to drop synthetic
// coupling edges by their Props["coupling_kind"] (see facts.Coupling* constants).
// A dependency fact whose coupling_kind is in excludeKinds contributes no edge.
// The cycles explainer uses this to exclude ActiveRecord associations, whose
// inherent bidirectionality would otherwise manufacture false cycles. With no
// excludeKinds it is identical to BuildModuleGraph.
func BuildModuleGraphExcluding(store *facts.Store, excludeKinds ...string) map[string][]string {
	graph := make(map[string][]string)

	var excluded map[string]bool
	if len(excludeKinds) > 0 {
		excluded = make(map[string]bool, len(excludeKinds))
		for _, k := range excludeKinds {
			excluded[k] = true
		}
	}

	modules := store.ByKind(facts.KindModule)
	moduleNames := make(map[string]bool)
	testModules := make(map[string]bool)
	for _, m := range modules {
		if isTestModule(m) {
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
		if excluded != nil {
			if ck, _ := dep.Props[facts.PropCouplingKind].(string); excluded[ck] {
				continue
			}
		}

		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			target := rel.Target

			if strings.HasPrefix(target, ".") {
				target = ResolveRelativeImport(sourceModule, target)
			}

			// A target is included only if it names a known internal module. This gate is
			// authoritative; we deliberately do NOT pre-filter with IsExternalImport, which
			// misclassifies single-segment internal modules (top-level "cmd", "config") as
			// external and would drop real edges before this check.
			if moduleNames[target] {
				graph[sourceModule] = append(graph[sourceModule], target)
			}
		}
	}

	return graph
}

// rubyFrameworkBaseClasses are exact Rails/framework base-class names whose high
// fan-in comes from being inherited, not from being a god class.
var rubyFrameworkBaseClasses = map[string]bool{
	"ApplicationRecord": true, "ApplicationController": true, "ApplicationJob": true,
	"ApplicationMailer": true, "ApplicationService": true, "ApplicationSerializer": true,
	"ApplicationCable": true, "ApplicationPolicy": true, "ApplicationInteractor": true,
	"ApplicationPresenter": true,
}

// rubyBaseClassSuffixes are naming conventions for user-defined base classes
// (NotifierBase, ApiBaseController, Pusher::Base, ...). A class named like this is
// a base others inherit from, so its inbound degree is inheritance, not coupling.
var rubyBaseClassSuffixes = []string{
	"BaseController", "BaseJob", "BaseService", "BaseMailer", "BaseSerializer",
	"BasePolicy", "BasePresenter", "BaseInteractor", "Base",
}

// IsRubyFrameworkBaseSymbol reports whether a symbol is a Rails/framework base
// class — one whose high fan-in is a product of inheritance (every subclass
// "depends on" it) rather than a design smell. Gated on a .rb file so non-Ruby
// symbols are never affected. Used by the god-class and hotspots explainers to keep
// framework scaffolding (ApplicationRecord/Controller/Job, *BaseController, *::Base)
// out of their findings while still surfacing genuine central domain types.
func IsRubyFrameworkBaseSymbol(name, file string) bool {
	if !strings.HasSuffix(file, ".rb") {
		return false
	}
	seg := name
	if i := strings.LastIndex(seg, "::"); i >= 0 {
		seg = seg[i+2:]
	}
	if rubyFrameworkBaseClasses[seg] {
		return true
	}
	for _, suffix := range rubyBaseClassSuffixes {
		if strings.HasSuffix(seg, suffix) {
			return true
		}
	}
	return false
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
