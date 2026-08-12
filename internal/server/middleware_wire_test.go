package server_test

// The middlewares registered with AddReceivingMiddleware are only useful if the SDK
// actually routes a tool call through them, and nothing else in the suite checks that.
//
// update_middleware_test.go calls the middleware function directly with a hardcoded
// "tools/call", which proves what the middleware DOES but not that it is ever reached.
// e2e_test.go goes over a real transport but only asserts tool-handler output, which a
// bypassed middleware would not change. So both suites stay green if the SDK stops
// dispatching through receiving middleware — and the loss is silent: the freshness banner
// simply stops appearing, and every tool call stops being reported to the status callback.
//
// This test closes that gap end to end. It is a dependency guard, not a feature test: the
// go-sdk v1.7.0 bump rewrote the wire protocol for MCP 2026-07-28 (server/discover
// replacing initialize, a stateless model, multi-round-trip requests), and "tools/call"
// surviving that rewrite as the dispatched method name is an assumption all three of
// enola's middlewares rest on.

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/status"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestReceivingMiddlewareRunsOverTheWire asserts that a tool call issued by a real client
// session reaches the middleware chain. valueMiddleware is the probe because its effect is
// observable without a snapshot on disk: it reports every completed call to the callback
// installed by SetToolCallback, and it only does so on the "tools/call" branch.
//
// All three middlewares are registered the same way, so this covers the dispatch
// mechanism for freshnessMiddleware and updateMiddleware too.
func TestReceivingMiddlewareRunsOverTheWire(t *testing.T) {
	ctx := context.Background()
	eng, cfg := newTestEngine(t)

	// Set up inline rather than reusing e2e_test.go's connect(), which returns only the
	// client session — SetToolCallback needs the server side too.
	srv, err := bootstrap.NewServer(eng, cfg)
	if err != nil {
		t.Fatalf("bootstrap.NewServer: %v", err)
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.MCP().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "enola-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	calls := make(chan status.ToolCall, 4)
	srv.SetToolCallback(func(c status.ToolCall) { calls <- c })

	// query_facts with no snapshot loaded returns a tool-level error, which is fine:
	// valueMiddleware reports the call either way, and dispatch is what is under test.
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "query_facts",
		Arguments: map[string]any{"kind": "module"},
	}); err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}

	select {
	case got := <-calls:
		if got.Tool != "query_facts" {
			t.Errorf("callback reported tool %q, want %q", got.Tool, "query_facts")
		}
	default:
		t.Fatal("no tool call reached valueMiddleware — the SDK is not dispatching " +
			"\"tools/call\" through AddReceivingMiddleware. The freshness banner and the " +
			"status callback are both silently dead in this state; check whether the " +
			"go-sdk bump renamed the method or changed middleware dispatch.")
	}
}
