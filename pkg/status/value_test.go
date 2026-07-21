package status

import (
	"testing"
	"time"
)

func TestComputeValue(t *testing.T) {
	counts := map[string]int{
		"generate_snapshot": 2, // weight 100 -> 200 ops
		"query_facts":       3, // weight 8   -> 24 ops
		"mystery_tool":      4, // unknown    -> defaultWeight 5 -> 20 ops
	}

	rep := ComputeValue(counts)

	// Tools are sorted by name.
	if len(rep.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(rep.Tools))
	}
	if rep.Tools[0].Tool != "generate_snapshot" {
		t.Errorf("expected first tool sorted as generate_snapshot, got %q", rep.Tools[0].Tool)
	}

	// Unknown tool falls back to defaultWeight.
	var mystery ToolValue
	for _, tv := range rep.Tools {
		if tv.Tool == "mystery_tool" {
			mystery = tv
		}
	}
	if mystery.ManualOpsAvoided != 4*defaultWeight {
		t.Errorf("mystery_tool ops: got %d, want %d", mystery.ManualOpsAvoided, 4*defaultWeight)
	}

	// Totals: 200 + 24 + 20 = 244 manual ops.
	wantOps := 200 + 24 + 20
	if rep.TotalManualOps != wantOps {
		t.Errorf("TotalManualOps: got %d, want %d", rep.TotalManualOps, wantOps)
	}
	if rep.TotalCalls != 9 {
		t.Errorf("TotalCalls: got %d, want 9", rep.TotalCalls)
	}
	wantTime := time.Duration(wantOps) * secondsPerManualOp * time.Second
	if rep.TotalTimeSaved != wantTime {
		t.Errorf("TotalTimeSaved: got %s, want %s", rep.TotalTimeSaved, wantTime)
	}
	if rep.TotalTokensSaved != wantOps*tokensPerManualOp {
		t.Errorf("TotalTokensSaved: got %d, want %d", rep.TotalTokensSaved, wantOps*tokensPerManualOp)
	}
}

func TestComputeValueEmpty(t *testing.T) {
	rep := ComputeValue(map[string]int{})
	if len(rep.Tools) != 0 || rep.TotalCalls != 0 || rep.TotalTokensSaved != 0 {
		t.Errorf("empty counts should yield zero-value report, got %+v", rep)
	}
}

func TestWeightForKnownTools(t *testing.T) {
	// Every tool in the catalog should have an explicit (non-default) weight so
	// the value model is intentional rather than falling through.
	for _, tool := range []string{
		"generate_snapshot", "explore", "query_facts", "show_symbol", "traverse",
		"find_path", "impact_analysis", "coverage_report", "query_insights",
		"set_baseline", "diff_snapshot", "snapshot_receipt", "compare_receipts",
	} {
		if _, ok := toolWeights[tool]; !ok {
			t.Errorf("tool %q has no explicit weight in toolWeights", tool)
		}
	}
}

func TestRegisterToolWeights(t *testing.T) {
	const tool = "wrapper_only_tool"
	t.Cleanup(func() {
		weightsMu.Lock()
		delete(toolWeights, tool)
		weightsMu.Unlock()
	})

	if got := weightFor(tool); got != defaultWeight {
		t.Fatalf("unregistered tool weight: got %d, want defaultWeight %d", got, defaultWeight)
	}

	RegisterToolWeights(map[string]int{tool: 42})
	if got := weightFor(tool); got != 42 {
		t.Errorf("registered tool weight: got %d, want 42", got)
	}

	// A registration must not disturb the built-in weights.
	if got := weightFor("generate_snapshot"); got != 100 {
		t.Errorf("generate_snapshot weight after registration: got %d, want 100", got)
	}
}
