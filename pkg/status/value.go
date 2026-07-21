package status

import (
	"sort"
	"sync"
	"time"
)

// The value model converts raw tool-call counts into two human-meaningful
// factors: time saved and tokens (context) saved. Each tool is assigned a
// single weight — the number of manual operations (file reads, greps, symbol
// lookups) a caller would otherwise perform to get the same answer. Two global
// constants convert that weight into wall-clock time and token estimates.
//
// These are deliberately estimates: they are derived entirely from call counts
// and a static table, with no changes to the engine itself. Tune the numbers
// freely — the model is one weight per tool plus two conversion constants.
const (
	// secondsPerManualOp is the assumed wall-clock cost of one manual lookup
	// (open a file, read it, decide) that a single tool call replaces.
	secondsPerManualOp = 30

	// tokensPerManualOp is the assumed context cost of one manual lookup —
	// roughly the tokens of an average source file an agent would read.
	tokensPerManualOp = 2000

	// defaultWeight is used for any tool name not in toolWeights, so the value
	// model degrades gracefully if the tool set grows.
	defaultWeight = 5
)

// toolWeights maps each tool to the number of manual operations one call of it
// replaces. It covers the tools this engine registers (see pkg/cli.OSSTools);
// a wrapper binary that adds its own MCP tools prices them via
// RegisterToolWeights rather than by editing this table.
//
// weightsMu guards the map: registration happens once at startup, but reads run
// from the MCP server's tool callback and, in wrapper binaries, from concurrent
// HTTP handlers.
var (
	weightsMu   sync.RWMutex
	toolWeights = map[string]int{
		"show_symbol":       3,
		"snapshot_receipt":  3,
		"set_baseline":      4,
		"query_facts":       8,
		"compare_receipts":  10,
		"traverse":          10,
		"find_path":         12,
		"explore":           15,
		"diff_snapshot":     15,
		"coverage_report":   20,
		"impact_analysis":   25,
		"query_insights":    30,
		"generate_snapshot": 100,
	}
)

// RegisterToolWeights merges caller-supplied per-tool weights into the value
// model, so a wrapper binary that registers additional MCP tools can price them
// instead of letting them fall back to defaultWeight. Call it during startup,
// before the server begins serving. Later registrations win for a given tool.
func RegisterToolWeights(extra map[string]int) {
	weightsMu.Lock()
	defer weightsMu.Unlock()
	for tool, w := range extra {
		toolWeights[tool] = w
	}
}

// weightFor returns the manual-ops-avoided weight for a tool. This is the single
// lookup point for the value model; a future config- or license-sourced override
// (e.g. per-customer tuning or usage gating) would slot in here.
func weightFor(tool string) int {
	weightsMu.RLock()
	defer weightsMu.RUnlock()
	if w, ok := toolWeights[tool]; ok {
		return w
	}
	return defaultWeight
}

// ToolValue is the estimated value delivered by a single tool.
type ToolValue struct {
	Tool             string
	Calls            int
	ManualOpsAvoided int
	TimeSaved        time.Duration
	TokensSaved      int
}

// ValueReport aggregates per-tool value plus totals.
type ValueReport struct {
	Tools            []ToolValue // sorted by tool name
	TotalCalls       int
	TotalManualOps   int
	TotalTimeSaved   time.Duration
	TotalTokensSaved int
}

// ComputeValue turns raw tool-call counts into an estimated value report.
func ComputeValue(counts map[string]int) ValueReport {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)

	var rep ValueReport
	rep.Tools = make([]ToolValue, 0, len(names))
	for _, name := range names {
		calls := counts[name]
		ops := calls * weightFor(name)
		tv := ToolValue{
			Tool:             name,
			Calls:            calls,
			ManualOpsAvoided: ops,
			TimeSaved:        time.Duration(ops) * secondsPerManualOp * time.Second,
			TokensSaved:      ops * tokensPerManualOp,
		}
		rep.Tools = append(rep.Tools, tv)
		rep.TotalCalls += calls
		rep.TotalManualOps += ops
		rep.TotalTimeSaved += tv.TimeSaved
		rep.TotalTokensSaved += tv.TokensSaved
	}
	return rep
}
