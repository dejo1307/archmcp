package cycles

import (
	"context"
	"fmt"
	"strings"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/facts"
)

// CycleExplainer detects cyclic dependencies between modules using Tarjan's SCC algorithm.
type CycleExplainer struct{}

// New creates a new CycleExplainer.
func New() *CycleExplainer {
	return &CycleExplainer{}
}

func (e *CycleExplainer) Name() string {
	return "cycles"
}

// Explain builds a dependency graph from import relations and detects cycles.
func (e *CycleExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	// Build adjacency list from dependency facts
	graph := common.BuildModuleGraph(store)

	// Run Tarjan's SCC
	sccs := tarjanSCC(graph)

	// Filter to cycles (SCCs with size > 1)
	var insights []facts.Insight
	for _, scc := range sccs {
		if len(scc) <= 1 {
			continue
		}

		cyclePath := strings.Join(scc, " -> ") + " -> " + scc[0]
		evidence := make([]facts.Evidence, 0, len(scc))
		for _, mod := range scc {
			evidence = append(evidence, facts.Evidence{
				Fact:   mod,
				Detail: fmt.Sprintf("module %q is part of the cycle", mod),
			})
		}

		insights = append(insights, facts.Insight{
			Title:       fmt.Sprintf("Cyclic dependency detected (%d modules)", len(scc)),
			Description: fmt.Sprintf("The following modules form a dependency cycle: %s. This can cause initialization issues, make refactoring harder, and indicates tight coupling.", cyclePath),
			Confidence:  1.0, // Deterministic
			Evidence:    evidence,
			Actions: []string{
				"Introduce an interface to break the cycle",
				"Extract shared types to a separate package",
				"Consider merging tightly coupled modules",
			},
		})
	}

	return insights, nil
}

// tarjanSCC computes strongly connected components of the module graph. It
// delegates to common.StronglyConnectedComponents, whose output is deterministic
// (sorted components with sorted members) — so the emitted cycle path, evidence
// order, and multi-cycle insight order no longer depend on Go's randomized map
// iteration.
func tarjanSCC(graph map[string][]string) [][]string {
	return common.StronglyConnectedComponents(graph)
}
