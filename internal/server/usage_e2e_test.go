package server_test

// End-to-end cover for the usage tracking behind `enola --status`: it wires a
// status.Tracker as the server's tool callback exactly as cmd/enola/main.go
// does, drives real tool calls over the in-memory transport, and asserts the
// counters that --status renders actually landed on disk.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/status"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestE2E_ToolUsageIsRecordedForStatus(t *testing.T) {
	// status writes under ~/.enola/usage/; keep the developer's real stats out of it.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	ctx := context.Background()
	eng, cfg := newTestEngine(t)

	srv, err := bootstrap.NewServer(eng, cfg)
	if err != nil {
		t.Fatalf("bootstrap.NewServer: %v", err)
	}

	repo := copyTree(t, filepath.Join("..", "engine", "testdata", "repos", "go_sample"), t.TempDir())
	tracker := status.NewTracker(repo)
	tracker.SetStartTime(time.Now())
	srv.SetToolCallback(tracker.Record)
	tracker.PersistStartup()

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.MCP().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "enola-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	// A snapshot must exist before query_facts returns anything, so this both
	// sets up the second call and is itself a recorded call.
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "generate_snapshot", Arguments: map[string]any{"repo_path": repo},
	}); err != nil {
		t.Fatalf("generate_snapshot: %v", err)
	}
	for range 2 {
		if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "query_facts", Arguments: map[string]any{"kind": "module"},
		}); err != nil {
			t.Fatalf("query_facts: %v", err)
		}
	}

	ss := status.ServerSnapshot()
	if !ss.Found {
		t.Fatal("no usage recorded; --status would report nothing after three tool calls")
	}
	for tool, want := range map[string]int{"generate_snapshot": 1, "query_facts": 2} {
		if got := ss.GrandTotal[tool]; got != want {
			t.Errorf("lifetime count for %q: got %d, want %d", tool, got, want)
		}
		if got := ss.Session[tool]; got != want {
			t.Errorf("session count for %q: got %d, want %d", tool, got, want)
		}
	}

	// The value estimate is what makes --status more than a counter dump.
	rep := status.ComputeValue(ss.GrandTotal, ss.GrandSaved)
	if rep.TotalCalls != 3 || rep.TotalTokensSaved == 0 {
		t.Errorf("value report over recorded usage: %d calls, %d tokens saved", rep.TotalCalls, rep.TotalTokensSaved)
	}

	// Credit must be recorded per call, not re-derived from counts at render
	// time — that is what keeps the figure identical across binaries.
	if ss.GrandSaved["generate_snapshot"] == 0 {
		t.Error("generate_snapshot recorded no corpus-derived credit")
	}
	if ss.GrandSaved["query_facts"] == 0 {
		t.Error("query_facts recorded no credit")
	}
}

// A restarted server restores its graph from disk and serves queries without
// re-snapshotting — that is what AutoLoadSnapshot exists for. Those queries
// searched a graph of a known size, so they must be priced against it. Before
// SeedCorpus the pool was empty until the first snapshot, so the normal path
// (restart, query, never snapshot) silently scored queries as if the graph were
// tiny.
func TestE2E_SeededCorpusPricesQueriesAfterRestart(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())

	eng, cfg := newTestEngine(t)
	srv, err := bootstrap.NewServer(eng, cfg)
	if err != nil {
		t.Fatalf("bootstrap.NewServer: %v", err)
	}

	repo := copyTree(t, filepath.Join("..", "engine", "testdata", "repos", "go_sample"), t.TempDir())
	tracker := status.NewTracker(repo)
	tracker.SetStartTime(time.Now())
	srv.SetToolCallback(tracker.Record)
	tracker.PersistStartup()

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.MCP().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "enola-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	// Stand in for a restored graph: facts loaded, and a corpus large enough that
	// pricing against it is visibly different from pricing against nothing.
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "generate_snapshot", Arguments: map[string]any{"repo_path": repo},
	}); err != nil {
		t.Fatalf("generate_snapshot: %v", err)
	}
	srv.SeedCorpus(map[string]int{repo: 40_000_000})

	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "query_facts", Arguments: map[string]any{"kind": "module", "output_mode": "summary"},
	}); err != nil {
		t.Fatalf("query_facts: %v", err)
	}

	ss := status.ServerSnapshot()
	got := ss.GrandSaved["query_facts"]
	unscaled := status.ComputeValue(
		map[string]int{"query_facts": 1}, map[string]int{},
	).TotalTokensSaved
	if got <= unscaled {
		t.Errorf("query on a seeded 40M-token graph earned %d, no more than the unscaled %d", got, unscaled)
	}
}

// A tool that fails is still a call that happened, but it displaced no work.
// This holds end-to-end only because recording runs after the handler: a read
// tool called with no snapshot loaded must earn nothing for its error message.
func TestE2E_FailedCallCountedButNotCredited(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())

	eng, cfg := newTestEngine(t)
	srv, err := bootstrap.NewServer(eng, cfg)
	if err != nil {
		t.Fatalf("bootstrap.NewServer: %v", err)
	}

	tracker := status.NewTracker(t.TempDir())
	tracker.SetStartTime(time.Now())
	srv.SetToolCallback(tracker.Record)
	tracker.PersistStartup()

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.MCP().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "enola-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	// No snapshot has been generated, so this returns "No facts available".
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "query_insights", Arguments: map[string]any{},
	}); err != nil {
		t.Fatalf("query_insights: %v", err)
	}

	ss := status.ServerSnapshot()
	if got := ss.GrandTotal["query_insights"]; got != 1 {
		t.Errorf("failed call should still be counted: got %d, want 1", got)
	}
	if got := ss.GrandSaved["query_insights"]; got != 0 {
		t.Errorf("failed call must earn no credit: got %d, want 0", got)
	}
}
