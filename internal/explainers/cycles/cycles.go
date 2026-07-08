package cycles

import (
	"context"
	"fmt"
	"strings"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/facts"
)

// maxCycleModules is the largest SCC still reported as a discrete, actionable
// "Cyclic dependency". Larger components are not a fixable cycle — in an
// autoloaded language (Ruby/Rails) mutual constant references across many
// directories are the expected topology, so a 99-module SCC is a coupling-density
// signal, not a defect. Such components are reported once as a softer,
// lower-confidence "Highly coupled module cluster" note instead of an alarming
// confidence-1.0 cycle with advice ("introduce an interface") that cannot
// meaningfully be applied to a 99-node cluster. Shared with the depth explainer
// via common.OversizedClusterModules.
const maxCycleModules = common.OversizedClusterModules

// maxClusterMembers caps how many representative members are listed as evidence
// for an oversized coupling cluster.
const maxClusterMembers = 12

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
	// Build adjacency list from dependency facts. ActiveRecord associations are
	// excluded: has_many/belongs_to pairs are inherently bidirectional domain
	// relationships, not load-order dependencies, and would otherwise manufacture
	// spurious two-module cycles between associated models.
	graph := common.BuildModuleGraphExcluding(store, facts.CouplingAssociation)

	// Run Tarjan's SCC
	sccs := tarjanSCC(graph)

	// Filter to cycles (SCCs with size > 1)
	var insights []facts.Insight
	for _, scc := range sccs {
		if len(scc) <= 1 {
			continue
		}

		if len(scc) > maxCycleModules {
			insights = append(insights, coupledClusterInsight(scc))
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
			// Title prefix is parsed by pkg/explain (Code health section); keep stable.
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

// coupledClusterInsight reports an oversized SCC as a soft coupling signal rather
// than a discrete cycle. The title deliberately does NOT start with "Cyclic
// dependency" so pkg/explain does not fold it into the cycle count.
func coupledClusterInsight(scc []string) facts.Insight {
	members := scc
	if len(members) > maxClusterMembers {
		members = members[:maxClusterMembers]
	}
	evidence := make([]facts.Evidence, 0, len(members))
	for _, mod := range members {
		evidence = append(evidence, facts.Evidence{
			Fact:   mod,
			Detail: fmt.Sprintf("module %q is part of the cluster", mod),
		})
	}
	return facts.Insight{
		Title: fmt.Sprintf("Highly coupled module cluster (%d modules)", len(scc)),
		Description: fmt.Sprintf(
			"%d modules reference each other mutually. In an autoloaded codebase "+
				"(e.g. Rails) this is expected — constant references between directories "+
				"resolve lazily, so this is not a load-order cycle. Treat it as an overall "+
				"coupling-density signal, not a defect to break.",
			len(scc),
		),
		Confidence: 0.4,
		Evidence:   evidence,
		Actions: []string{
			"Look for a few high-traffic modules whose extraction would thin the cluster",
			"Prefer narrowing individual module responsibilities over a single big refactor",
		},
	}
}

// tarjanSCC computes strongly connected components of the module graph. It
// delegates to common.StronglyConnectedComponents, whose output is deterministic
// (sorted components with sorted members) — so the emitted cycle path, evidence
// order, and multi-cycle insight order no longer depend on Go's randomized map
// iteration.
func tarjanSCC(graph map[string][]string) [][]string {
	return common.StronglyConnectedComponents(graph)
}
