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
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// CoverageExplainer emits insights about detected-but-unresolved outbound edges.
type CoverageExplainer struct{}

// New creates a new CoverageExplainer.
func New() *CoverageExplainer {
	return &CoverageExplainer{}
}

// maxUnresolvedCallers caps the breakdown, matching the sibling explainers.
const maxUnresolvedCallers = 8

// unresolvedCallers names the components whose outbound calls match no route in
// any loaded repository, most-calls-first.
func unresolvedCallers(store *facts.Store, repo string) []facts.Evidence {
	served := map[string]bool{}
	for _, fact := range store.ByKind(facts.KindRoute) {
		if role, _ := fact.Props["role"].(string); role == "client" {
			continue
		}
		served[routeKey(fact)] = true
	}

	byComponent := map[string]int{}
	for _, fact := range store.ByKind(facts.KindRoute) {
		if fact.Repo != repo {
			continue
		}
		if role, _ := fact.Props["role"].(string); role != "client" {
			continue
		}
		if isDouble, _ := fact.Props["test_double"].(bool); isDouble {
			continue
		}
		if served[routeKey(fact)] {
			continue
		}
		component := fact.File
		if idx := strings.LastIndex(component, "/"); idx > 0 {
			component = component[:idx]
		}
		byComponent[component]++
	}
	if len(byComponent) == 0 {
		return nil
	}

	components := make([]string, 0, len(byComponent))
	for component := range byComponent {
		components = append(components, component)
	}
	sort.Slice(components, func(i, j int) bool {
		if byComponent[components[i]] != byComponent[components[j]] {
			return byComponent[components[i]] > byComponent[components[j]]
		}
		return components[i] < components[j]
	})
	if len(components) > maxUnresolvedCallers {
		components = components[:maxUnresolvedCallers]
	}

	out := make([]facts.Evidence, 0, len(components))
	for _, component := range components {
		out = append(out, facts.Evidence{
			Fact:   component,
			Detail: fmt.Sprintf("%d unresolved outbound call site(s) from here", byComponent[component]),
		})
	}
	return out
}

// routeKey compares a client's path dialect against a server's.
func routeKey(fact facts.Fact) string {
	method, _ := fact.Props["method"].(string)
	segments := strings.Split(fact.Name, "/")
	for i, segment := range segments {
		if segment == "{}" || strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			segments[i] = ":param"
		}
	}
	return strings.ToUpper(method) + " " + strings.TrimSuffix(strings.Join(segments, "/"), "/")
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

		outbound := facts.DependsOnCount(svc)
		evidence := []facts.Evidence{{Fact: svc.Name, Detail: coverageDetail(cov)}}
		// "External target or unloaded repo" is the honest ambiguity, and it is
		// also the whole question: one means add a repository to the cluster, the
		// other means this is a third party and nothing is missing. Naming the
		// components that make the unresolved calls answers it on sight —
		// lib/opensearch is not a repository anyone forgot.
		evidence = append(evidence, unresolvedCallers(store, svc.Repo)...)

		var insight facts.Insight
		if facts.ClassifyService(outbound, detected, unresolved) == facts.ServiceCoverageGap {
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
	external   int
}

// coverageDetail renders the per-edge-type counts for an insight description. The
// external count (calls to hardcoded third-party hosts) is shown only when present,
// since it is expected rather than a blind spot.
func coverageDetail(cov []coverageEntry) string {
	out := ""
	for i, c := range cov {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s %d/%d resolved (%d unresolved", c.edgeType, c.resolved, c.detected, c.unresolved)
		if c.external > 0 {
			out += fmt.Sprintf(", %d external", c.external)
		}
		out += ")"
	}
	return out
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
			external:   asInt(m["external"]),
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
