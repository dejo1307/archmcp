// Package hotspots flags "pinch point" symbols that sit on many call paths —
// symbols with both high fan-in and high fan-out. These chokepoints are where
// most call chains converge and re-fan-out, so they are disproportionately
// risky to change and valuable to test.
//
// This is a cheap degree-centrality proxy for betweenness centrality: rather
// than the O(V·E) Brandes algorithm over the whole call graph, it scores each
// node by fanIn × fanOut, which strongly correlates with being on many paths.
package hotspots

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/factpath"
	"github.com/enola-labs/enola/internal/facts"
)

const (
	// minDegree is the floor each of fan-in and fan-out must meet; a true pinch
	// point funnels and re-distributes, so both sides must be non-trivial.
	minDegree = 3
	// stdDevK is how many standard deviations above the mean score a symbol must
	// sit to qualify as an outlier.
	stdDevK = 2.0
	// maxNeighbors caps how many in/out neighbors are listed as evidence.
	maxNeighbors = 5
	// maxInsights caps how many pinch points are reported, highest score first.
	// This explainer dominates finding volume — roughly 80% of all findings
	// across the upstream corpus are hotspots (docs/EXPLAINERS.md) — and an
	// uncapped listing buries every other explainer's signal under one
	// explainer's ranking. 20 matches the largest sibling cap (exported-surface);
	// the outlier threshold already keeps the set small on most repositories, so
	// the cap only bites where the volume was pure noise. Ties beyond the cap
	// cut deterministically: the sort below breaks equal scores by name.
	maxInsights = 20
)

// HotspotExplainer detects degree-centrality pinch points.
type HotspotExplainer struct{}

// New creates a new HotspotExplainer.
func New() *HotspotExplainer {
	return &HotspotExplainer{}
}

func (e *HotspotExplainer) Name() string {
	return "hotspots"
}

// Explain scores symbols by fanIn × fanOut and reports statistical outliers.
func (e *HotspotExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	graph := store.Graph()
	if graph == nil {
		return nil, nil
	}
	// Degrees are asked per symbol rather than by pulling whole adjacency maps out of
	// the graph: the graph stores its adjacency as CSR, and the map form used to
	// build a full filtered copy of the reverse index on every call.
	//
	// Architectural degree on both sides. Fan-in: reference-only facts
	// (test_ref/file_ref) are not symbols, so counting their RelCalls edges inflates
	// the centrality score and the outlier distribution (GAP-XL-15). Fan-out: the
	// has_method edges wiring a type to the methods it declares are containment, not
	// calls, and this explainer says out loud that they are calls — so counting them
	// reports a class as a pinch point for being large. Raw out-degree put
	// BaseImporter, whose 449 lines are 102 one-line delegations and exactly one call
	// out, in the monolith's top 20 as "it calls out to 104 others".
	symbols := store.ByKind(facts.KindSymbol)
	if len(symbols) == 0 {
		return nil, nil
	}

	// Report each distinct symbol name once. A constant reopened across many files
	// (Ruby STI/concerns, monkey-patched framework namespaces) yields one symbol
	// fact per file, all sharing a Name and therefore identical in/out degree.
	// De-dupe here so both the outlier distribution and the candidate set are over
	// distinct symbols (repeated values would otherwise skew the threshold).
	distinct := make([]facts.Fact, 0, len(symbols))
	seen := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		distinct = append(distinct, s)
	}

	scores := make(map[string]int, len(distinct))
	values := make([]float64, 0, len(distinct))
	for _, s := range distinct {
		in := graph.ArchitecturalFanIn(s.Name)
		out := graph.ResolvedCallFanOut(s.Name)
		score := in * out
		scores[s.Name] = score
		values = append(values, float64(score))
	}

	threshold := common.OutlierThreshold(values, stdDevK)

	type candidate struct {
		fact  facts.Fact
		in    int
		out   int
		score int
	}
	var candidates []candidate
	for _, s := range distinct {
		// A Rails/framework base class has high fan-in via inheritance, not because
		// it is a genuine pinch point — skip it (non-Ruby symbols are unaffected).
		if common.IsRubyFrameworkBaseSymbol(s.Name, s.File) {
			continue
		}
		// Test-support code is not production architecture. See the god-class
		// explainer for the full rationale: ArchitecturalReverse drops reference-only
		// KINDS (and RelInstantiates edges), but a Swift/Kotlin/Python test file is indexed as an ordinary symbol
		// fact, so it needs a path gate too (GAP-XL-15). Candidate-only: the score and
		// the outlier distribution stay whole.
		if facts.IsTestPath(s.File) {
			continue
		}
		in := graph.ArchitecturalFanIn(s.Name)
		out := graph.ResolvedCallFanOut(s.Name)
		if in < minDegree || out < minDegree {
			continue
		}
		if float64(scores[s.Name]) <= threshold {
			continue
		}
		candidates = append(candidates, candidate{fact: s, in: in, out: out, score: scores[s.Name]})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].fact.Name < candidates[j].fact.Name
	})
	if len(candidates) > maxInsights {
		candidates = candidates[:maxInsights]
	}

	modules := make(map[string]bool)
	for _, module := range store.ByKind(facts.KindModule) {
		modules[module.Name] = true
	}

	var insights []facts.Insight
	for _, c := range candidates {
		evidence := []facts.Evidence{{
			Symbol: c.fact.Name,
			File:   c.fact.File,
			Detail: fmt.Sprintf("fan-in %d × fan-out %d = score %d", c.in, c.out, c.score),
		}}
		for _, edge := range firstN(graph.ArchitecturalReverseEdges(c.fact.Name), maxNeighbors) {
			evidence = append(evidence, facts.Evidence{Symbol: edge.Target, Detail: "calls into " + c.fact.Name})
		}
		for _, edge := range firstN(graph.ResolvedCallForwardEdges(c.fact.Name), maxNeighbors) {
			evidence = append(evidence, facts.Evidence{Symbol: edge.Target, Detail: "called by " + c.fact.Name})
		}

		insights = append(insights, facts.Insight{
			// Title format is parsed by pkg/explain (Code health section); keep stable.
			Title: fmt.Sprintf("Call-graph hotspot: %s (fan-in %d, fan-out %d)", c.fact.Name, c.in, c.out),
			Description: fmt.Sprintf(
				"%q is a pinch point: %d symbols call into it and it calls out to %d others, so a large "+
					"share of call chains pass through it (degree-centrality proxy for betweenness). "+
					"Such chokepoints are high-risk to change and high-value to test.",
				c.fact.Name, c.in, c.out,
			),
			Confidence: 0.7,
			Evidence:   evidence,
			Metrics:    fanOutMetrics(store, c.fact, modules),
			Actions: []string{
				"Add focused tests around this chokepoint before refactoring",
				"Consider decomposing it so call paths don't all funnel through one symbol",
				"Verify it isn't doing orchestration that belongs in callers",
			},
		})
	}

	return insights, nil
}

const (
	MetricUniqueCallees               = "unique_callees"
	MetricTypesInstantiated           = "types_instantiated"
	MetricEnumVariantsConstructed     = "enum_variants_constructed"
	MetricDataTypesReferenced         = "data_types_referenced"
	MetricExternalPackageDependencies = "external_package_dependencies"
	MetricInternalModuleDependencies  = "internal_module_dependencies"
)

func fanOutMetrics(store *facts.Store, symbol facts.Fact, modules map[string]bool) map[string]any {
	sets := map[string]map[string]bool{
		MetricUniqueCallees: {}, MetricTypesInstantiated: {}, MetricEnumVariantsConstructed: {},
		MetricDataTypesReferenced: {}, MetricExternalPackageDependencies: {}, MetricInternalModuleDependencies: {},
	}
	for _, rel := range symbol.Relations {
		switch rel.Kind {
		case facts.RelCalls:
			for _, target := range store.ByName(rel.Target) {
				if target.Kind == facts.KindSymbol {
					sets[MetricUniqueCallees][rel.Target] = true
					break
				}
			}
		case facts.RelInstantiates:
			sets[MetricTypesInstantiated][rel.Target] = true
		case facts.RelConstructsVariant:
			sets[MetricEnumVariantsConstructed][rel.Target] = true
		case facts.RelReferencesType:
			sets[MetricDataTypesReferenced][rel.Target] = true
		}
	}
	for _, f := range store.ByFile(symbol.File) {
		if f.Kind != facts.KindDependency {
			continue
		}
		source, _ := f.Props["source"].(string)
		for _, rel := range f.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			switch source {
			case facts.DepSourceExternal:
				sets[MetricExternalPackageDependencies][dependencyRoot(rel.Target)] = true
			case facts.DepSourceInternal:
				if module := enclosingModule(rel.Target, modules); module != "" {
					sets[MetricInternalModuleDependencies][module] = true
				}
			}
		}
	}
	metrics := make(map[string]any, len(sets))
	for key, values := range sets {
		metrics[key] = len(values)
	}
	return metrics
}

func enclosingModule(target string, modules map[string]bool) string {
	for cur := target; cur != "." && cur != ""; cur = factpath.Dir(cur) {
		if modules[cur] {
			return cur
		}
		next := factpath.Dir(cur)
		if next == cur {
			break
		}
	}
	return ""
}

func dependencyRoot(target string) string {
	if i := strings.Index(target, "::"); i >= 0 {
		return target[:i]
	}
	if i := strings.IndexByte(target, '/'); i >= 0 {
		return target[:i]
	}
	return target
}

func firstN(edges []facts.Edge, n int) []facts.Edge {
	if len(edges) <= n {
		// Copy so callers can't mutate the graph's slice; also gives stable order.
		out := make([]facts.Edge, len(edges))
		copy(out, edges)
		sortEdges(out)
		return out
	}
	out := make([]facts.Edge, len(edges))
	copy(out, edges)
	sortEdges(out)
	return out[:n]
}

func sortEdges(edges []facts.Edge) {
	sort.Slice(edges, func(i, j int) bool { return edges[i].Target < edges[j].Target })
}
