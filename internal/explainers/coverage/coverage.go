// Package coverage provides an explainer that turns the per-service edge_coverage
// counts recorded by internal/linkers/crossrepo into "coverage gap" insights.
//
// A service node with no outbound dependencies can mean two very different things:
// it is genuinely a leaf, or enola detected outbound call sites it could not
// resolve to a target. This explainer surfaces the latter so a human knows where
// the map is thin and worth verifying against source, rather than assuming the
// service is isolated.
package coverage

import (
	"context"
	"fmt"

	"github.com/enola-labs/enola/internal/facts"
)

// CoverageExplainer emits insights about detected-but-unresolved outbound edges.
type CoverageExplainer struct{}

// New creates a new CoverageExplainer.
func New() *CoverageExplainer {
	return &CoverageExplainer{}
}

func (e *CoverageExplainer) Name() string {
	return "coverage"
}

// Explain reads the edge_coverage props on service nodes and emits one insight per
// service that has unresolved outbound call sites, distinguishing a service that
// appears isolated (no resolved outbound edges at all) from one that is merely
// partially covered. It returns nothing for single-repo snapshots (no services).
func (e *CoverageExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	var insights []facts.Insight
	for _, svc := range store.ByKind(facts.KindService) {
		cov := readCoverage(svc)
		var unresolved, detected int
		for _, c := range cov {
			detected += c.detected
			unresolved += c.unresolved
		}
		if unresolved == 0 {
			continue
		}

		outbound := dependsOnCount(svc)
		evidence := []facts.Evidence{{Fact: svc.Name, Detail: coverageDetail(cov)}}

		var insight facts.Insight
		if outbound == 0 {
			insight = facts.Insight{
				Title: fmt.Sprintf("Coverage gap: service %s appears isolated but has %d unresolved outbound call site(s)",
					svc.Name, unresolved),
				Description: fmt.Sprintf("enola detected %d outbound call site(s) from %s but could not resolve "+
					"%d of them to a loaded service, and the service has no resolved outbound dependencies. "+
					"It may not be isolated — verify these call sites against source. Details: %s.",
					detected, svc.Name, unresolved, coverageDetail(cov)),
				Confidence: 0.9,
				Evidence:   evidence,
				Actions: []string{
					"Verify the unresolved call sites against source",
					"Check whether the called service is in the snapshot (append its repo if missing)",
				},
			}
		} else {
			insight = facts.Insight{
				Title: fmt.Sprintf("Partial coverage: service %s has %d unresolved outbound call site(s)",
					svc.Name, unresolved),
				Description: fmt.Sprintf("enola resolved some of %s's outbound dependencies but %d of %d detected "+
					"call site(s) did not resolve to a loaded service (external target or unloaded repo). Details: %s.",
					svc.Name, unresolved, detected, coverageDetail(cov)),
				Confidence: 0.75,
				Evidence:   evidence,
			}
		}
		insights = append(insights, insight)
	}
	return insights, nil
}

// coverageEntry is one edge_type's tally, read back tolerantly from a service node.
type coverageEntry struct {
	edgeType   string
	detected   int
	resolved   int
	unresolved int
}

// coverageDetail renders the per-edge-type counts for an insight description.
func coverageDetail(cov []coverageEntry) string {
	out := ""
	for i, c := range cov {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s %d/%d resolved (%d unresolved)", c.edgeType, c.resolved, c.detected, c.unresolved)
	}
	return out
}

// dependsOnCount returns how many resolved outbound (cross-repo) dependencies a
// service node carries.
func dependsOnCount(svc facts.Fact) int {
	n := 0
	for _, rel := range svc.Relations {
		if rel.Kind == facts.RelDependsOn {
			n++
		}
	}
	return n
}

// readCoverage extracts the edge_coverage entries from a service node's props,
// tolerating both the in-memory shape ([]map[string]any with int values) and the
// shape that survives a facts.jsonl JSON round-trip ([]any of map[string]any with
// float64 values).
func readCoverage(svc facts.Fact) []coverageEntry {
	if svc.Props == nil {
		return nil
	}
	var raw []map[string]any
	switch v := svc.Props["edge_coverage"].(type) {
	case []map[string]any:
		raw = v
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				raw = append(raw, m)
			}
		}
	default:
		return nil
	}

	out := make([]coverageEntry, 0, len(raw))
	for _, m := range raw {
		edgeType, _ := m["edge_type"].(string)
		out = append(out, coverageEntry{
			edgeType:   edgeType,
			detected:   asInt(m["detected"]),
			resolved:   asInt(m["resolved"]),
			unresolved: asInt(m["unresolved"]),
		})
	}
	return out
}

// asInt reads an int-valued prop, tolerating the float64 form that survives a JSON
// round-trip through facts.jsonl.
func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}
