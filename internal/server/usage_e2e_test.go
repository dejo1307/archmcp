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
	srv.SetToolCallback(tracker.OnToolCall)
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
	if rep := status.ComputeValue(ss.GrandTotal); rep.TotalCalls != 3 || rep.TotalTokensSaved == 0 {
		t.Errorf("value report over recorded usage: %d calls, %d tokens saved", rep.TotalCalls, rep.TotalTokensSaved)
	}
}
