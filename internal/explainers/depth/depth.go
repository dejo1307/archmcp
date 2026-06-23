// Package depth flags modules that sit too deep in the import chain — i.e.
// whose longest transitive dependency path is unusually long. Deep modules are
// slow to understand and load-bearing: a change at the bottom of a long chain
// can force rebuilds/retests all the way up.
package depth

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/facts"
)

const (
	// minDepth is the longest-chain length at or above which a module is
	// reported. A chain of N means N modules are imported in sequence below it.
	minDepth = 5
	// maxInsights caps how many deep modules are reported, deepest first.
	maxInsights = 10
)

// DepthExplainer detects modules deep in the dependency chain.
type DepthExplainer struct{}

// New creates a new DepthExplainer.
func New() *DepthExplainer {
	return &DepthExplainer{}
}

func (e *DepthExplainer) Name() string {
	return "dependency-depth"
}

// Explain builds the module import graph and computes each module's longest
// downstream dependency chain (cycle-safe), reporting the deepest ones.
func (e *DepthExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	graph := common.BuildModuleGraph(store)
	if len(graph) == 0 {
		return nil, nil
	}

	memo := make(map[string][]string) // module -> deepest chain starting at module
	visiting := make(map[string]bool)
	for mod := range graph {
		longestChain(mod, graph, memo, visiting)
	}

	type result struct {
		module string
		chain  []string
	}
	var results []result
	for mod, chain := range memo {
		if len(chain) >= minDepth {
			results = append(results, result{module: mod, chain: chain})
		}
	}

	// Deepest first; break ties by name for determinism.
	sort.Slice(results, func(i, j int) bool {
		if len(results[i].chain) != len(results[j].chain) {
			return len(results[i].chain) > len(results[j].chain)
		}
		return results[i].module < results[j].module
	})

	var insights []facts.Insight
	for i, r := range results {
		if i >= maxInsights {
			break
		}
		depth := len(r.chain)
		evidence := make([]facts.Evidence, 0, len(r.chain))
		for _, m := range r.chain {
			evidence = append(evidence, facts.Evidence{Fact: m})
		}

		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Deep dependency chain: %s (depth %d)", r.module, depth),
			Description: fmt.Sprintf(
				"Module %q has a longest dependency chain of %d modules: %s. "+
					"Deep chains slow comprehension and widen rebuild/retest impact when a "+
					"module near the bottom changes.",
				r.module, depth, strings.Join(r.chain, " -> "),
			),
			Confidence: 0.7,
			Evidence:   evidence,
			Actions: []string{
				"Flatten the chain by depending on shared abstractions instead of deep transitive modules",
				"Check whether intermediate modules are pass-through layers that can be removed",
				"Introduce interfaces to decouple the deepest modules from their consumers",
			},
		})
	}

	return insights, nil
}

// longestChain returns the longest dependency chain starting at module, as a
// slice beginning with module. Results are memoized. The visiting set breaks
// cycles: a back-edge to a module already on the current path contributes no
// further depth, so cyclic graphs terminate.
func longestChain(module string, graph map[string][]string, memo map[string][]string, visiting map[string]bool) []string {
	if chain, ok := memo[module]; ok {
		return chain
	}
	if visiting[module] {
		// Cycle back-edge: stop here without recursing.
		return []string{module}
	}
	visiting[module] = true

	var best []string
	for _, dep := range graph[module] {
		if dep == module {
			continue // self-import; ignore
		}
		child := longestChain(dep, graph, memo, visiting)
		if len(child) > len(best) {
			best = child
		}
	}

	visiting[module] = false
	chain := append([]string{module}, best...)
	memo[module] = chain
	return chain
}
