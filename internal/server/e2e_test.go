package server_test

// End-to-end tests for the MCP tool surface — the contract AI agents actually
// hit. The existing server_test.go (package server) exercises internal helper
// methods against hand-built stores; this file instead spins up the real
// engine+server over an in-memory MCP transport and drives each registered tool
// through the wire (JSON args in, CallToolResult out), so the tool handlers,
// arg unmarshaling, name resolution, and result rendering are all covered.
//
// Fixtures are shared with the engine golden tests (../engine/testdata/repos).
// We use go_sample because the Go extractor is stdlib-based and its fact graph
// is small and stable: modules ".", "pkg/a", "pkg/b"; symbols "..main",
// "pkg/a.Alpha", "pkg/b.Beta"; with a deliberate pkg/a<->pkg/b import cycle.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// expectedTools is the OSS MCP tool surface. If a tool is added or renamed,
// this list (and the per-tool coverage below) should move with it.
var expectedTools = []string{
	"generate_snapshot",
	"query_facts",
	"explore",
	"show_symbol",
	"traverse",
	"find_path",
	"impact_analysis",
	"coverage_report",
}

// session bundles a connected client and the temp repo the server was pointed at.
type session struct {
	cs   *mcp.ClientSession
	repo string
}

// startInMemory wires bootstrap.NewEngine + NewServer to an in-memory transport
// and returns a connected client session plus a fresh temp copy of go_sample.
func startInMemory(t *testing.T) *session {
	t.Helper()
	ctx := context.Background()

	eng, cfg, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: filepath.Join(t.TempDir(), "no-such-config.yaml"),
	})
	if err != nil {
		t.Fatalf("bootstrap.NewEngine: %v", err)
	}
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

	repo := copyTree(t, filepath.Join("..", "engine", "testdata", "repos", "go_sample"), t.TempDir())
	return &session{cs: cs, repo: repo}
}

// call invokes a tool and fails the test on transport error (a transport error
// is distinct from a tool-level IsError, which several assertions check for).
func (s *session) call(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) transport error: %v", name, err)
	}
	return res
}

// text concatenates the text content of a tool result.
func text(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// snapshot runs generate_snapshot against the session's temp repo so the
// server's engine is populated before the other tools are exercised.
func (s *session) snapshot(t *testing.T) {
	t.Helper()
	res := s.call(t, "generate_snapshot", map[string]any{"repo_path": s.repo})
	if res.IsError {
		t.Fatalf("generate_snapshot returned error: %s", text(res))
	}
	if !strings.Contains(text(res), "Facts:") {
		t.Fatalf("generate_snapshot summary missing 'Facts:'; got:\n%s", text(res))
	}
}

func TestE2E_ListTools(t *testing.T) {
	s := startInMemory(t)
	res, err := s.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, name := range expectedTools {
		if !got[name] {
			t.Errorf("expected tool %q to be registered; registered: %v", name, keys(got))
		}
	}
}

func TestE2E_GenerateAndQuery(t *testing.T) {
	s := startInMemory(t)
	s.snapshot(t)

	// query_facts(kind=module) must surface the fixture's modules.
	out := text(s.call(t, "query_facts", map[string]any{"kind": "module"}))
	for _, mod := range []string{"pkg/a", "pkg/b"} {
		if !strings.Contains(out, mod) {
			t.Errorf("query_facts(kind=module) missing %q; got:\n%s", mod, out)
		}
	}

	// kinds= batch filter is OR within the dimension.
	out = text(s.call(t, "query_facts", map[string]any{"kinds": []string{"symbol"}, "output_mode": "names"}))
	if !strings.Contains(out, "Alpha") || !strings.Contains(out, "Beta") {
		t.Errorf("query_facts(kinds=[symbol]) missing known symbols; got:\n%s", out)
	}
}

func TestE2E_ShowSymbol(t *testing.T) {
	s := startInMemory(t)
	s.snapshot(t)

	out := text(s.call(t, "show_symbol", map[string]any{"name": "Alpha", "context_lines": 10}))
	if !strings.Contains(out, "Alpha") {
		t.Errorf("show_symbol(Alpha) missing symbol name; got:\n%s", out)
	}
	if !strings.Contains(out, "a.go") {
		t.Errorf("show_symbol(Alpha) missing source file reference; got:\n%s", out)
	}
}

func TestE2E_Explore(t *testing.T) {
	s := startInMemory(t)
	s.snapshot(t)

	res := s.call(t, "explore", map[string]any{"focus": "pkg/a"})
	if res.IsError {
		t.Fatalf("explore(pkg/a) errored: %s", text(res))
	}
	if !strings.Contains(text(res), "Alpha") {
		t.Errorf("explore(pkg/a) should mention symbol Alpha; got:\n%s", text(res))
	}
}

func TestE2E_Traverse(t *testing.T) {
	s := startInMemory(t)
	s.snapshot(t)

	// pkg/b imports pkg/a, so traversing reverse from pkg/a must reach pkg/b.
	out := text(s.call(t, "traverse", map[string]any{
		"start": "pkg/a", "direction": "reverse", "output_mode": "compact",
	}))
	if !strings.Contains(out, "pkg/b") {
		t.Errorf("traverse(reverse, pkg/a) should reach pkg/b; got:\n%s", out)
	}
}

func TestE2E_FindPath(t *testing.T) {
	s := startInMemory(t)
	s.snapshot(t)

	// Positive: main calls Alpha, so a forward path exists.
	res := s.call(t, "find_path", map[string]any{"from": "..main", "to": "pkg/a.Alpha"})
	if res.IsError {
		t.Fatalf("find_path(main -> Alpha) errored: %s", text(res))
	}
	if !strings.Contains(text(res), "Alpha") {
		t.Errorf("find_path(main -> Alpha) should report a path through Alpha; got:\n%s", text(res))
	}

	// Negative: Beta only reaches Alpha/Beta (the cycle), never main, so there
	// is no forward path. This must be a graceful "no path" answer, not an error.
	res = s.call(t, "find_path", map[string]any{"from": "pkg/b.Beta", "to": "..main"})
	if res.IsError {
		t.Fatalf("find_path with no route should not be a tool error; got:\n%s", text(res))
	}
	if !strings.Contains(strings.ToLower(text(res)), "no path") {
		t.Errorf("find_path(Beta -> main) should report no path; got:\n%s", text(res))
	}
}

func TestE2E_ImpactAnalysis(t *testing.T) {
	s := startInMemory(t)
	s.snapshot(t)

	// main and Beta both call Alpha, so changing Alpha impacts 2 dependents.
	// Summary mode reports the count + hotspot modules; compact mode names them.
	out := text(s.call(t, "impact_analysis", map[string]any{"target": "pkg/a.Alpha"}))
	if !strings.Contains(out, "2 total") {
		t.Errorf("impact_analysis(Alpha) should report 2 dependents; got:\n%s", out)
	}
	out = text(s.call(t, "impact_analysis", map[string]any{"target": "pkg/a.Alpha", "output_mode": "compact"}))
	if !strings.Contains(out, "Beta") {
		t.Errorf("impact_analysis(Alpha, compact) should list Beta as a dependent; got:\n%s", out)
	}
}

func TestE2E_CoverageReport(t *testing.T) {
	s := startInMemory(t)
	s.snapshot(t)

	// coverage_report is registered in OSS; on a single Go repo it should return
	// a non-error report (its content varies, so we only assert it succeeds).
	res := s.call(t, "coverage_report", map[string]any{})
	if res.IsError {
		t.Fatalf("coverage_report errored: %s", text(res))
	}
	if strings.TrimSpace(text(res)) == "" {
		t.Errorf("coverage_report returned empty output")
	}
}

// TestE2E_RequiredArgValidation checks that tools with required arguments fail
// (as a tool error or a transport error) when the required arg is omitted,
// rather than silently succeeding.
func TestE2E_RequiredArgValidation(t *testing.T) {
	s := startInMemory(t)
	s.snapshot(t)

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"explore", map[string]any{}},
		{"traverse", map[string]any{}},
		{"find_path", map[string]any{"from": "pkg/a"}}, // missing "to"
		{"impact_analysis", map[string]any{}},
		{"show_symbol", map[string]any{}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			res, err := s.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
			if err != nil {
				return // transport-level rejection is an acceptable failure mode
			}
			if !res.IsError {
				t.Errorf("%s with missing required arg should be an error; got:\n%s", c.tool, text(res))
			}
		})
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// copyTree recursively copies src into a fresh subdir of dstParent and returns
// the new root. Kept local to this package (the engine golden harness has its
// own copy) so the e2e tests stay self-contained.
func copyTree(t *testing.T, src, dstParent string) string {
	t.Helper()
	dst := filepath.Join(dstParent, filepath.Base(src))
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read fixture dir %s: %v", src, err)
	}
	for _, e := range entries {
		sp := filepath.Join(src, e.Name())
		if e.IsDir() {
			copyTree(t, sp, dst)
			continue
		}
		data, err := os.ReadFile(sp)
		if err != nil {
			t.Fatalf("read %s: %v", sp, err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dst
}
