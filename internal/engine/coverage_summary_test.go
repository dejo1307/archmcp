package engine

// coverageSummary rolls up per-service edge_coverage into the snapshot-level
// CoverageSummary. These tests pin that external call sites are surfaced separately
// and excluded from the internal blind-spot count (unresolved) and gap tally.

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func svcCoverage(name string, resolved, unresolved, external int) facts.Fact {
	return facts.Fact{
		Kind: facts.KindService,
		Name: name,
		Repo: name,
		Props: map[string]any{
			"synthetic": "crossrepo",
			"edge_coverage": []map[string]any{{
				"edge_type":  "http_client",
				"detected":   resolved + unresolved + external,
				"resolved":   resolved,
				"unresolved": unresolved,
				"external":   external,
			}},
		},
	}
}

func TestCoverageSummary_ExternalBucket(t *testing.T) {
	st := facts.NewStore()
	st.Add(
		svcCoverage("a", 5, 2, 0),  // 2 internal unresolved -> a coverage gap
		svcCoverage("b", 4, 0, 3),  // only external -> NOT a gap
		svcCoverage("c", 1, 1, 4),  // both -> gap, external counted separately
	)

	sum := coverageSummary(st)
	if sum == nil {
		t.Fatal("expected a CoverageSummary")
	}
	if sum.ServicesTotal != 3 {
		t.Errorf("ServicesTotal = %d, want 3", sum.ServicesTotal)
	}
	if sum.CoverageGaps != 2 {
		t.Errorf("CoverageGaps = %d, want 2 (external-only service is not a gap)", sum.CoverageGaps)
	}
	if sum.UnresolvedEdges != 3 {
		t.Errorf("UnresolvedEdges = %d, want 3 (internal only: 2+1)", sum.UnresolvedEdges)
	}
	if sum.ExternalEdges != 7 {
		t.Errorf("ExternalEdges = %d, want 7 (3+4)", sum.ExternalEdges)
	}
}
