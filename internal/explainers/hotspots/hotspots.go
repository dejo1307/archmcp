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

	"github.com/enola-labs/enola/internal/explainers/common"
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
	forward := graph.Forward()
	reverse := graph.Reverse()

	symbols := store.ByKind(facts.KindSymbol)
	if len(symbols) == 0 {
		return nil, nil
	}

	scores := make(map[string]int, len(symbols))
	values := make([]float64, 0, len(symbols))
	for _, s := range symbols {
		in := len(reverse[s.Name])
		out := len(forward[s.Name])
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
	for _, s := range symbols {
		in := len(reverse[s.Name])
		out := len(forward[s.Name])
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

	var insights []facts.Insight
	for _, c := range candidates {
		evidence := []facts.Evidence{{
			Symbol: c.fact.Name,
			File:   c.fact.File,
			Detail: fmt.Sprintf("fan-in %d × fan-out %d = score %d", c.in, c.out, c.score),
		}}
		for _, edge := range firstN(reverse[c.fact.Name], maxNeighbors) {
			evidence = append(evidence, facts.Evidence{Symbol: edge.Target, Detail: "calls into " + c.fact.Name})
		}
		for _, edge := range firstN(forward[c.fact.Name], maxNeighbors) {
			evidence = append(evidence, facts.Evidence{Symbol: edge.Target, Detail: "called by " + c.fact.Name})
		}

		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Call-graph hotspot: %s (fan-in %d, fan-out %d)", c.fact.Name, c.in, c.out),
			Description: fmt.Sprintf(
				"%q is a pinch point: %d symbols call into it and it calls out to %d others, so a large "+
					"share of call chains pass through it (degree-centrality proxy for betweenness). "+
					"Such chokepoints are high-risk to change and high-value to test.",
				c.fact.Name, c.in, c.out,
			),
			Confidence: 0.7,
			Evidence:   evidence,
			Actions: []string{
				"Add focused tests around this chokepoint before refactoring",
				"Consider decomposing it so call paths don't all funnel through one symbol",
				"Verify it isn't doing orchestration that belongs in callers",
			},
		})
	}

	return insights, nil
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
