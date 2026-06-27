package server

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func sampleInsights() []facts.Insight {
	return []facts.Insight{
		{
			Source:     "unused-routes",
			Title:      "Unused endpoint candidates: 562 route(s) in golf have no caller among loaded clients",
			Confidence: 0.6,
			Evidence: []facts.Evidence{
				{Fact: "/api/settings/recommendations", File: "golf/internal/bootstrap/server.go", Detail: "no loaded client calls this route"},
			},
			Actions: []string{"Verify against out-of-snapshot consumers before removing."},
		},
		{
			Source:     "cycles",
			Title:      "Circular dependency among 3 modules in golf-ui",
			Confidence: 0.9,
			Evidence:   []facts.Evidence{{Fact: "golf-ui/src/a"}},
		},
		{
			Source:     "god-class",
			Title:      "High fan-in symbol",
			Confidence: 0.5,
		},
	}
}

func TestFilterInsights(t *testing.T) {
	all := sampleInsights()

	if got := filterInsights(all, "unused-routes", "", 0); len(got) != 1 || got[0].Source != "unused-routes" {
		t.Errorf("explainer filter: want 1 unused-routes insight, got %+v", got)
	}
	if got := filterInsights(all, "UNUSED-ROUTES", "", 0); len(got) != 1 {
		t.Errorf("explainer filter should be case-insensitive; got %d", len(got))
	}
	if got := filterInsights(all, "", "", 0.8); len(got) != 1 || got[0].Source != "cycles" {
		t.Errorf("min_confidence filter: want only the 0.9 cycles insight, got %+v", got)
	}
	// repo matches via title ("in golf") and evidence file prefix.
	if got := filterInsights(all, "", "golf-ui", 0); len(got) != 1 || got[0].Source != "cycles" {
		t.Errorf("repo filter golf-ui: want the cycles insight, got %+v", got)
	}
	if got := filterInsights(all, "", "nonexistent", 0); got != nil {
		t.Errorf("repo filter with no match should be empty; got %+v", got)
	}
	if got := filterInsights(all, "", "", 0); len(got) != 3 {
		t.Errorf("no filters should return all; got %d", len(got))
	}
}

func TestRenderInsightsSummary(t *testing.T) {
	out := renderInsightsSummary(sampleInsights())
	for _, want := range []string{"Found **3** insight(s)", "## By explainer", "unused-routes", "0.60", "Unused endpoint candidates"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q; got:\n%s", want, out)
		}
	}
}

func TestRenderInsightsCompact(t *testing.T) {
	out := renderInsightsCompact(filterInsights(sampleInsights(), "unused-routes", "", 0))
	for _, want := range []string{"explainer: unused-routes", "confidence: 0.60", "no loaded client calls this route", "suggested actions"} {
		if !strings.Contains(out, want) {
			t.Errorf("compact missing %q; got:\n%s", want, out)
		}
	}
}

func TestRenderQuerySummary_SurfacesUnmatchedFlag(t *testing.T) {
	results := []facts.Fact{
		{Kind: facts.KindRoute, Name: "/a", Props: map[string]any{"unmatched_by_clients": true}},
		{Kind: facts.KindRoute, Name: "/b", Props: map[string]any{"unmatched_by_clients": true}},
		{Kind: facts.KindRoute, Name: "/c"},
	}
	out := renderQuerySummary(results, len(results))
	if !strings.Contains(out, "## Flags") || !strings.Contains(out, "unmatched_by_clients=true: 2") {
		t.Errorf("summary should surface the unmatched_by_clients flag count; got:\n%s", out)
	}
}
