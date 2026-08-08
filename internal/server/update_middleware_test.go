package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/updatecheck"
	"github.com/enola-labs/enola/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// seedUpdate isolates HOME and plants a cached manifest advertising a newer release,
// so updatecheck has something to report without any network access.
//
// HOME is re-pointed per test even though TestMain already sandboxes it, because these
// tests WRITE into ~/.enola and would otherwise leak state into each other through the
// shared package-level temp home.
func seedUpdate(t *testing.T, extractorVersion string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(updatecheck.DisableEnv, "")
	t.Setenv("CI", "")

	prev := version.Version
	version.Version = "0.3.2"
	t.Cleanup(func() { version.Version = prev })

	dir := filepath.Join(home, ".enola")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"checked_at":        time.Now().UTC(),
		"version":           "0.3.12",
		"extractor_version": extractorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// callTool drives one tool call through the middleware with a handler that returns a
// fixed successful result.
func callTool(t *testing.T, s *Server, tool string) *mcp.CallToolResult {
	t.Helper()
	handler := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "tool output"}},
		}, nil
	}
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: tool}}

	result, err := s.updateMiddleware(handler)(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("middleware returned error: %v", err)
	}
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("result is %T, want *mcp.CallToolResult", result)
	}
	return ctr
}

// text flattens a result's content for assertions.
func text(ctr *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range ctr.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// Once per server process, not once per call. A server process is a session, so the
// agent hears this on its first tool call and is left alone for the rest of the work;
// repeating it would make it wallpaper and dilute the freshness banner, which DOES need
// to repeat because it is actionable immediately.
func TestUpdateMiddlewareSpeaksOnceThenStaysQuiet(t *testing.T) {
	seedUpdate(t, "v999")
	s := &Server{}

	first := text(callTool(t, s, "query_facts"))
	if !strings.Contains(first, "A newer enola is available") {
		t.Fatalf("first call carried no update notice:\n%s", first)
	}
	if !strings.Contains(first, "tool output") {
		t.Errorf("notice replaced the tool's own output instead of joining it:\n%s", first)
	}
	// Appended, not prepended: the freshness warning is the one an agent may need to
	// act on before trusting the result, so it keeps the top of the response.
	if strings.HasPrefix(first, "ℹ️") {
		t.Errorf("notice was prepended; it must not displace the freshness banner:\n%s", first)
	}

	for i := range 3 {
		if got := text(callTool(t, s, "query_facts")); strings.Contains(got, "A newer enola is available") {
			t.Fatalf("notice repeated on call %d:\n%s", i+2, got)
		}
	}
}

// The two tools the freshness banner also skips: one has just refreshed the graph, the
// other is a mutation rather than a query. Neither is a place for an aside.
func TestUpdateMiddlewareRespectsBannerSuppression(t *testing.T) {
	for _, tool := range []string{"generate_snapshot", "set_baseline"} {
		t.Run(tool, func(t *testing.T) {
			seedUpdate(t, "v999")
			s := &Server{}
			if got := text(callTool(t, s, tool)); strings.Contains(got, "A newer enola is available") {
				t.Errorf("%s carried an update notice:\n%s", tool, got)
			}
			// …and the suppressed call must not have BURNED the one-shot, or the notice
			// would be lost entirely for a session that happens to snapshot first.
			if got := text(callTool(t, s, "query_facts")); !strings.Contains(got, "A newer enola is available") {
				t.Errorf("suppressed call consumed the once-per-process notice:\n%s", got)
			}
		})
	}
}

func TestUpdateMiddlewareSilentWhenUpToDate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(updatecheck.DisableEnv, "")
	t.Setenv("CI", "")

	// No cache at all — the state a fresh install is in, and the one every offline
	// machine stays in.
	s := &Server{}
	if got := text(callTool(t, s, "query_facts")); strings.Contains(got, "newer enola") {
		t.Errorf("notice appeared with no cached manifest:\n%s", got)
	}
}

// An error result is the agent's problem to solve; padding it with housekeeping makes
// the actual failure harder to read.
func TestUpdateMiddlewareSkipsErrorResults(t *testing.T) {
	seedUpdate(t, "v999")
	s := &Server{}

	handler := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "boom"}},
		}, nil
	}
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "query_facts"}}
	result, err := s.updateMiddleware(handler)(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := text(result.(*mcp.CallToolResult)); strings.Contains(got, "newer enola") {
		t.Errorf("notice attached to an error result:\n%s", got)
	}
}

// The MCP SDK dispatches tool calls concurrently, so the one-shot has to be claimed
// atomically. A check-then-set would let two simultaneous calls both append.
func TestUpdateMiddlewareOneShotIsRaceFree(t *testing.T) {
	seedUpdate(t, "v999")
	s := &Server{}

	var mu sync.Mutex
	notices := 0
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler := func(context.Context, string, mcp.Request) (mcp.Result, error) {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "tool output"}},
				}, nil
			}
			req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "query_facts"}}
			result, err := s.updateMiddleware(handler)(context.Background(), "tools/call", req)
			if err != nil {
				return
			}
			if strings.Contains(text(result.(*mcp.CallToolResult)), "A newer enola is available") {
				mu.Lock()
				notices++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if notices != 1 {
		t.Errorf("attached the notice %d times across concurrent calls, want exactly 1", notices)
	}
}

// The escalation is a claim about this graph's data, so it must only appear when the
// extractors really did move.
func TestUpdateMiddlewareExtractorEscalation(t *testing.T) {
	t.Run("moved", func(t *testing.T) {
		seedUpdate(t, "v999")
		if got := text(callTool(t, &Server{}, "query_facts")); !strings.Contains(got, "Extractors changed since this build") {
			t.Errorf("missing escalation when the extractor version moved:\n%s", got)
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		seedUpdate(t, engine.ExtractorVersion())
		got := text(callTool(t, &Server{}, "query_facts"))
		if !strings.Contains(got, "A newer enola is available") {
			t.Fatalf("expected the plain notice:\n%s", got)
		}
		if strings.Contains(got, "Extractors changed") {
			t.Errorf("claimed the extractors moved when they did not:\n%s", got)
		}
	})
}
