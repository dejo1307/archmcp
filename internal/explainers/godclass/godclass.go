// Package godclass flags symbols with an unusually high fan-in — i.e. that are
// depended upon by a large number of other symbols. Such "god" types/functions
// are change-risk concentrators: editing them ripples widely, and a high
// inbound degree often signals a missing abstraction boundary.
package godclass

import (
	"context"
	"fmt"
	"sort"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/facts"
)

const (
	// minFanIn is the floor below which a symbol is never reported, regardless
	// of the statistical threshold. Keeps small repos quiet.
	minFanIn = 8
	// stdDevK is how many standard deviations above the mean fan-in a symbol
	// must sit to qualify as an outlier.
	stdDevK = 2.0
	// maxEvidence caps how many dependents are listed as evidence per insight.
	maxEvidence = 8
	// maxInsights caps how many high-fan-in symbols are reported, matching the
	// sibling explainers (surface=20, complexity=15, depth=10). Without a cap a
	// large repo can emit hundreds of findings dominated by central-by-design
	// framework types.
	maxInsights = 25
)

// GodClassExplainer detects high-fan-in symbols.
type GodClassExplainer struct{}

// New creates a new GodClassExplainer.
func New() *GodClassExplainer {
	return &GodClassExplainer{}
}

func (e *GodClassExplainer) Name() string {
	return "god-class"
}

// Explain computes fan-in for every symbol from the reverse adjacency list and
// reports the statistical outliers.
func (e *GodClassExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	graph := store.Graph()
	if graph == nil {
		return nil, nil
	}
	// Architectural fan-in only: reference-only facts (test_ref/file_ref) carry
	// RelCalls edges into production code but are not symbols. Counting them
	// inflates fan-in and drifts the outlier threshold below (GAP-XL-15).
	reverse := graph.ArchitecturalReverse()

	symbols := store.ByKind(facts.KindSymbol)
	if len(symbols) == 0 {
		return nil, nil
	}

	// Report each distinct symbol name once. A constant reopened across many files
	// (Ruby STI/concerns, monkey-patched framework namespaces like
	// RailsAdmin::Config::Actions) yields one symbol fact per file, all sharing a
	// Name. Fan-in is keyed by name, so without de-duping they produced identical
	// findings AND skewed the outlier distribution below (the repeated high value
	// dragged the threshold up). De-dupe here so both the distribution and the
	// candidate set are over distinct symbols.
	distinct := make([]facts.Fact, 0, len(symbols))
	seen := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		distinct = append(distinct, s)
	}

	// Fan-in per symbol and the distribution used for outlier detection.
	fanIn := make(map[string]int, len(distinct))
	values := make([]float64, 0, len(distinct))
	for _, s := range distinct {
		n := len(reverse[s.Name])
		fanIn[s.Name] = n
		values = append(values, float64(n))
	}

	threshold := common.OutlierThreshold(values, stdDevK)

	type candidate struct {
		fact   facts.Fact
		fanIn  int
		labels []string
	}
	var candidates []candidate
	for _, s := range distinct {
		// A Rails/framework base class (ApplicationRecord, *BaseController, *::Base)
		// has high fan-in purely because every subclass inherits from it — that is
		// not a god class. Skip it (non-Ruby symbols are unaffected).
		if common.IsRubyFrameworkBaseSymbol(s.Name, s.File) {
			continue
		}
		n := fanIn[s.Name]
		if n < minFanIn || float64(n) <= threshold {
			continue
		}
		dependents := make([]string, 0, len(reverse[s.Name]))
		for _, edge := range reverse[s.Name] {
			dependents = append(dependents, edge.Target)
		}
		sort.Strings(dependents)
		candidates = append(candidates, candidate{fact: s, fanIn: n, labels: dependents})
	}

	// Most-depended-upon first for stable, prioritized output.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].fanIn != candidates[j].fanIn {
			return candidates[i].fanIn > candidates[j].fanIn
		}
		return candidates[i].fact.Name < candidates[j].fact.Name
	})

	if len(candidates) > maxInsights {
		candidates = candidates[:maxInsights]
	}

	var insights []facts.Insight
	for _, c := range candidates {
		evidence := make([]facts.Evidence, 0, maxEvidence+1)
		evidence = append(evidence, facts.Evidence{
			Symbol: c.fact.Name,
			File:   c.fact.File,
			Detail: fmt.Sprintf("%d symbols depend on this", c.fanIn),
		})
		for i, dep := range c.labels {
			if i >= maxEvidence {
				break
			}
			evidence = append(evidence, facts.Evidence{Symbol: dep, Detail: "depends on " + c.fact.Name})
		}

		insights = append(insights, facts.Insight{
			// Title format is parsed by pkg/explain (Code health section); keep stable.
			Title: fmt.Sprintf("High fan-in symbol: %s (%d dependents)", c.fact.Name, c.fanIn),
			Description: fmt.Sprintf(
				"%q is depended upon by %d other symbols — well above the repo average. "+
					"High fan-in concentrates change risk: edits here ripple across many call sites, "+
					"and it often indicates a missing or overly broad abstraction.",
				c.fact.Name, c.fanIn,
			),
			Confidence: confidence(float64(c.fanIn), threshold),
			Evidence:   evidence,
			Actions: []string{
				"Split the symbol along distinct responsibilities its callers use",
				"Introduce narrower interfaces so callers depend only on what they need",
				"Stabilize its public contract to limit churn for dependents",
			},
		})
	}

	return insights, nil
}

// confidence scales from 0.5 at the threshold toward 1.0 as fan-in grows to ~2x
// the threshold.
func confidence(value, threshold float64) float64 {
	if threshold <= 0 {
		return 0.6
	}
	ratio := value / threshold // >= 1 for reported symbols
	c := 0.5 + 0.5*(ratio-1)
	if c > 1 {
		c = 1
	}
	if c < 0.5 {
		c = 0.5
	}
	return c
}
