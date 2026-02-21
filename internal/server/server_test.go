package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dejo1307/archmcp/internal/config"
	"github.com/dejo1307/archmcp/internal/engine"
	"github.com/dejo1307/archmcp/internal/facts"
)

func TestReadSourceWindow(t *testing.T) {
	// Create a 10-line temp file
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line "+string(rune('0'+i)))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		centerLine   int
		contextLines int
		wantStart    int
		wantEnd      int
	}{
		{"center middle", 5, 6, 2, 8},
		{"center at start", 1, 10, 1, 6},
		{"center at end", 10, 10, 5, 10},
		{"context larger than file", 5, 20, 1, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readSourceWindow(path, tt.centerLine, tt.contextLines)
			if err != nil {
				t.Fatalf("readSourceWindow: %v", err)
			}

			outputLines := strings.Split(strings.TrimRight(got, "\n"), "\n")

			// Verify first line starts with expected line number
			firstLine := outputLines[0]
			if !strings.Contains(firstLine, "│") {
				t.Fatalf("expected line number format with │, got: %s", firstLine)
			}

			// Count output lines
			expectedCount := tt.wantEnd - tt.wantStart + 1
			if len(outputLines) != expectedCount {
				t.Errorf("got %d output lines, want %d (lines %d-%d)",
					len(outputLines), expectedCount, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestReadSourceWindow_SingleLineFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.go")
	if err := os.WriteFile(path, []byte("only line"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readSourceWindow(path, 1, 30)
	if err != nil {
		t.Fatalf("readSourceWindow: %v", err)
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line for single-line file, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "only line") {
		t.Errorf("expected output to contain 'only line', got: %s", lines[0])
	}
}

func TestReadSourceWindow_LineNumberFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readSourceWindow(path, 3, 4)
	if err != nil {
		t.Fatalf("readSourceWindow: %v", err)
	}

	// Should have format "   N│ content"
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.Contains(line, "│") {
			t.Errorf("line missing │ separator: %q", line)
		}
	}
}

// --- test helpers ---

// newEngineWithSnapshot creates an engine with a fake snapshot pointing at the given repo path.
func newEngineWithSnapshot(repoPath string) *engine.Engine {
	cfg := config.Default()
	eng, _ := engine.New(cfg)
	eng.SetSnapshot(&facts.Snapshot{
		Meta: facts.SnapshotMeta{RepoPath: repoPath},
	})
	return eng
}

// --- explore helper tests ---

// newTestServer creates a Server with a pre-populated fact store for testing explore methods.
func newTestServer(store *facts.Store) *Server {
	return &Server{}
}

func populateTestStore() *facts.Store {
	store := facts.NewStore()
	store.Add(
		// Module
		facts.Fact{Kind: facts.KindModule, Name: "internal/server", Props: map[string]any{"language": "go", "package": "server"}},
		// Symbols declared in that module
		facts.Fact{Kind: facts.KindSymbol, Name: "internal/server.New", File: "internal/server/server.go", Line: 26,
			Props:     map[string]any{"symbol_kind": "function", "exported": true, "language": "go"},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: "internal/server"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "internal/server.Run", File: "internal/server/server.go", Line: 45,
			Props:     map[string]any{"symbol_kind": "method", "exported": true, "language": "go"},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: "internal/server"}, {Kind: facts.RelCalls, Target: "internal/engine.Store"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "internal/server.handleQuery", File: "internal/server/handler.go", Line: 10,
			Props:     map[string]any{"symbol_kind": "function", "exported": false, "language": "go"},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: "internal/server"}, {Kind: facts.RelCalls, Target: "internal/facts.Store.Query"}}},
		// Another module
		facts.Fact{Kind: facts.KindModule, Name: "internal/facts", Props: map[string]any{"language": "go", "package": "facts"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "internal/facts.Store.Query", File: "internal/facts/store.go", Line: 105,
			Props:     map[string]any{"symbol_kind": "method", "exported": true, "language": "go"},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: "internal/facts"}}},
		// Dependency
		facts.Fact{Kind: facts.KindDependency, Name: "internal/server -> internal/facts", File: "internal/server/server.go",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "internal/facts"}}},
		// Symbol in a different directory
		facts.Fact{Kind: facts.KindSymbol, Name: "cmd.main", File: "cmd/main.go", Line: 1,
			Props: map[string]any{"symbol_kind": "function", "exported": false, "language": "go"}},
	)
	return store
}

func TestExploreModule(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreModule(store, "internal/server", 1, &sb)
	if !found {
		t.Fatal("exploreModule should find 'internal/server'")
	}

	output := sb.String()

	// Should contain the module header
	if !strings.Contains(output, "# Module: internal/server") {
		t.Error("missing module header")
	}
	// Should list symbols
	if !strings.Contains(output, "internal/server.New") {
		t.Error("missing symbol New")
	}
	if !strings.Contains(output, "internal/server.Run") {
		t.Error("missing symbol Run")
	}
	if !strings.Contains(output, "internal/server.handleQuery") {
		t.Error("missing symbol handleQuery")
	}
	// Should show dependents (who imports internal/server -> none in test data)
	// Should show the symbols table
	if !strings.Contains(output, "Symbols (3)") {
		t.Error("missing symbols count")
	}
}

func TestExploreModule_NotFound(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreModule(store, "nonexistent", 1, &sb)
	if found {
		t.Error("exploreModule should return false for nonexistent module")
	}
}

func TestExploreModule_Depth2(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreModule(store, "internal/server", 2, &sb)
	if !found {
		t.Fatal("exploreModule should find 'internal/server'")
	}

	output := sb.String()
	// Depth 2 should include symbol relations section
	if !strings.Contains(output, "Symbol Relations") {
		t.Error("depth=2 should include Symbol Relations section")
	}
	// Should show the calls relation from Run
	if !strings.Contains(output, "internal/engine.Store") {
		t.Error("depth=2 should show call targets")
	}
}

func TestExploreFile(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreFile(store, "internal/server/server.go", 1, &sb)
	if !found {
		t.Fatal("exploreFile should find 'internal/server/server.go'")
	}

	output := sb.String()
	if !strings.Contains(output, "# File: internal/server/server.go") {
		t.Error("missing file header")
	}
	// Should list the symbols in this file (New, Run) and the dependency
	if !strings.Contains(output, "internal/server.New") {
		t.Error("missing symbol New")
	}
	if !strings.Contains(output, "internal/server.Run") {
		t.Error("missing symbol Run")
	}
}

func TestExploreFile_NotFound(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreFile(store, "nonexistent.go", 1, &sb)
	if found {
		t.Error("exploreFile should return false for nonexistent file")
	}
}

func TestExploreSymbol(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreSymbol(store, "Store.Query", 1, &sb)
	if !found {
		t.Fatal("exploreSymbol should find 'Store.Query'")
	}

	output := sb.String()
	if !strings.Contains(output, "# Symbol: Store.Query") {
		t.Error("missing symbol header")
	}
	if !strings.Contains(output, "internal/facts/store.go") {
		t.Error("missing file reference")
	}
	// Should include Referenced By section (handleQuery calls it)
	if !strings.Contains(output, "Referenced By") {
		t.Error("missing Referenced By section")
	}
	if !strings.Contains(output, "internal/server.handleQuery") {
		t.Error("missing caller handleQuery in Referenced By")
	}
}

func TestExploreSymbol_NotFound(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreSymbol(store, "NonExistentSymbol", 1, &sb)
	if found {
		t.Error("exploreSymbol should return false for nonexistent symbol")
	}
}

func TestExploreDirectory(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreDirectory(store, "internal/server", &sb)
	if !found {
		t.Fatal("exploreDirectory should find 'internal/server'")
	}

	output := sb.String()
	if !strings.Contains(output, "# Directory: internal/server") {
		t.Error("missing directory header")
	}
	if !strings.Contains(output, "Summary") {
		t.Error("missing Summary section")
	}
}

func TestExploreDirectory_NotFound(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreDirectory(store, "nonexistent/dir", &sb)
	if found {
		t.Error("exploreDirectory should return false for nonexistent directory")
	}
}

// --- normalizeToRelative tests ---

func TestNormalizeToRelative_AbsolutePath(t *testing.T) {
	srv := &Server{
		eng: newEngineWithSnapshot("/Users/me/development"),
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"absolute subdir", "/Users/me/development/go-service", "go-service"},
		{"absolute file", "/Users/me/development/go-service/lib/foo.rb", "go-service/lib/foo.rb"},
		{"absolute repo root", "/Users/me/development", "."},
		{"already relative", "internal/server", "internal/server"},
		{"unrelated absolute", "/other/path/foo", "/other/path/foo"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := srv.normalizeToRelative(tt.input)
			if got != tt.want {
				t.Errorf("normalizeToRelative(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeToRelative_MultiRepo(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/workspace")
	eng.SetRepoPaths(map[string]string{
		"go-service":    "/Users/me/development/go-service",
		"ruby-monolith": "/Users/me/development/ruby-monolith",
	})
	srv := &Server{eng: eng}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"multi-repo go-service dir", "/Users/me/development/go-service", "go-service"},
		{"multi-repo go-service file", "/Users/me/development/go-service/lib/foo.rb", "go-service/lib/foo.rb"},
		{"multi-repo ruby-monolith file", "/Users/me/development/ruby-monolith/lib/bar.rb", "ruby-monolith/lib/bar.rb"},
		{"unrelated absolute", "/other/path/foo", "/other/path/foo"},
		{"relative passthrough", "internal/server", "internal/server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := srv.normalizeToRelative(tt.input)
			if got != tt.want {
				t.Errorf("normalizeToRelative(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Integration tests: original reported use cases ---

// TestScenario_QueryFactsWithFilePrefixCrossRepo simulates the first reported issue:
// query_facts with file_prefix like "go-service/..." or "ruby-monolith/..." returned nothing.
func TestScenario_QueryFactsWithFilePrefixCrossRepo(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/development/workspace")
	eng.SetRepoPaths(map[string]string{
		"go-service":    "/Users/me/development/go-service",
		"ruby-monolith": "/Users/me/development/ruby-monolith",
	})
	srv := &Server{eng: eng}

	store := eng.Store()

	// Simulate repo A facts (go-service) - files prefixed as in append mode
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "Pricing", File: "go-service/lib/pricing.rb", Repo: "go-service",
			Props: map[string]any{"language": "ruby"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "PricingService", File: "go-service/lib/pricing_service.rb", Line: 5, Repo: "go-service",
			Props:     map[string]any{"symbol_kind": "class", "exported": true, "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "CoreUtils"}}},
		facts.Fact{Kind: facts.KindDependency, Name: "go-service -> ruby-monolith", File: "go-service/lib/pricing_service.rb", Repo: "go-service",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "ruby-monolith"}}},
	)

	// Simulate repo B facts (ruby-monolith)
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "Core", File: "ruby-monolith/lib/core.rb", Repo: "ruby-monolith",
			Props: map[string]any{"language": "ruby"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "CoreUtils", File: "ruby-monolith/lib/utils.rb", Line: 10, Repo: "ruby-monolith",
			Props: map[string]any{"symbol_kind": "class", "exported": true, "language": "ruby"}},
	)

	// Test 1: query_facts with file_prefix "go-service" should find go-service facts
	results, total := store.QueryAdvanced(facts.QueryOpts{FilePrefix: "go-service"})
	if total != 3 {
		t.Errorf("file_prefix=go-service: total=%d, want 3", total)
	}
	for _, f := range results {
		if !strings.HasPrefix(f.File, "go-service") {
			t.Errorf("unexpected file %q in go-service results", f.File)
		}
	}

	// Test 2: query_facts with file_prefix "ruby-monolith" should find ruby-monolith facts
	results, total = store.QueryAdvanced(facts.QueryOpts{FilePrefix: "ruby-monolith"})
	if total != 2 {
		t.Errorf("file_prefix=ruby-monolith: total=%d, want 2", total)
	}

	// Test 3: repo filter should work
	results, total = store.QueryAdvanced(facts.QueryOpts{Repo: "go-service"})
	if total != 3 {
		t.Errorf("repo=go-service: total=%d, want 3", total)
	}

	// Test 4: normalize absolute path to file_prefix
	normalized := srv.normalizeToRelative("/Users/me/development/go-service/lib")
	if normalized != "go-service/lib" {
		t.Errorf("normalize(/Users/me/development/go-service/lib) = %q, want go-service/lib", normalized)
	}
	results, total = store.QueryAdvanced(facts.QueryOpts{FilePrefix: normalized})
	if total != 3 {
		t.Errorf("normalized file_prefix: total=%d, want 3 (module + symbol + dep all in go-service/lib/)", total)
	}
}

// TestScenario_ExploreWithAbsolutePathCrossRepo simulates the second reported issue:
// explore with focus "/Users/.../go-service" returned "No facts matching focus".
func TestScenario_ExploreWithAbsolutePathCrossRepo(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/workspace")
	eng.SetRepoPaths(map[string]string{
		"go-service":    "/Users/me/development/go-service",
		"ruby-monolith": "/Users/me/development/ruby-monolith",
	})
	srv := &Server{eng: eng}

	store := eng.Store()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "PricingService", File: "go-service/lib/pricing_service.rb", Line: 5, Repo: "go-service",
			Props: map[string]any{"symbol_kind": "class", "exported": true}},
		facts.Fact{Kind: facts.KindSymbol, Name: "CoreUtils", File: "ruby-monolith/lib/utils.rb", Line: 10, Repo: "ruby-monolith",
			Props: map[string]any{"symbol_kind": "class", "exported": true}},
	)

	// Test: explore with absolute path to go-service repo root
	focus := srv.normalizeToRelative("/Users/me/development/go-service")
	t.Logf("normalized focus: %q", focus)

	var sb strings.Builder
	found := srv.exploreDirectory(store, focus, &sb)
	if !found {
		t.Errorf("exploreDirectory should find facts for normalized focus %q", focus)
	}
	output := sb.String()
	if !strings.Contains(output, "PricingService") {
		t.Errorf("explore output should contain PricingService, got:\n%s", output)
	}

	// Test: explore with absolute path to ruby-monolith repo root
	focus = srv.normalizeToRelative("/Users/me/development/ruby-monolith")
	t.Logf("normalized focus: %q", focus)

	sb.Reset()
	found = srv.exploreDirectory(store, focus, &sb)
	if !found {
		t.Errorf("exploreDirectory should find facts for normalized focus %q", focus)
	}
	output = sb.String()
	if !strings.Contains(output, "CoreUtils") {
		t.Errorf("explore output should contain CoreUtils, got:\n%s", output)
	}

	// Test: explore with absolute path to subdirectory
	focus = srv.normalizeToRelative("/Users/me/development/go-service/lib")
	if focus != "go-service/lib" {
		t.Errorf("normalized subdir = %q, want go-service/lib", focus)
	}

	sb.Reset()
	found = srv.exploreDirectory(store, focus, &sb)
	if !found {
		t.Errorf("exploreDirectory should find facts for subdir focus %q", focus)
	}
}

// TestScenario_ExploreSingleRepoAbsolutePath tests the single-repo case where
// someone passes the repo root as an absolute path to explore.
func TestScenario_ExploreSingleRepoAbsolutePath(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/development/go-service")
	srv := &Server{eng: eng}

	store := eng.Store()
	// Use Go-style names with dots — these would be falsely matched if "."
	// were used as a substring query on symbol names.
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "pricing.Service", File: "lib/pricing_service.go", Line: 5,
			Props: map[string]any{"symbol_kind": "struct", "exported": true}},
		facts.Fact{Kind: facts.KindSymbol, Name: "pricing.Calculator", File: "lib/price_calculator.go", Line: 10,
			Props: map[string]any{"symbol_kind": "struct", "exported": true}},
	)

	// Normalize the exact repo root
	focus := srv.normalizeToRelative("/Users/me/development/go-service")
	t.Logf("single-repo root normalized to: %q", focus)

	// Raw exploreSymbol WOULD match "." as substring of "pricing.Service" etc.
	// This is the bug we're guarding against at the handler level.
	var sb strings.Builder
	rawSymbolMatch := srv.exploreSymbol(store, focus, 1, &sb)
	if !rawSymbolMatch {
		t.Log("(note: exploreSymbol didn't match — names may not contain dots)")
	} else {
		t.Log("exploreSymbol falsely matches '.' — handler switch must prevent this")
	}

	// The handler-level fix: exploreDirectory handles "." as repo root.
	// In the explore switch, "." routes directly to exploreDirectory, skipping exploreSymbol.
	sb.Reset()
	found := srv.exploreDirectory(store, focus, &sb)
	if !found {
		t.Errorf("exploreDirectory should handle %q as repo root", focus)
	}
	output := sb.String()
	if !strings.Contains(output, "pricing.Service") {
		t.Errorf("root directory explore should contain pricing.Service, got:\n%s", output)
	}
	// Verify it's a directory-style output (not a symbol dump)
	if !strings.Contains(output, "Directory:") {
		t.Error("root explore should produce Directory-style output")
	}

	// Normalize a subdirectory - should work
	focus = srv.normalizeToRelative("/Users/me/development/go-service/lib")
	if focus != "lib" {
		t.Errorf("normalized subdir = %q, want lib", focus)
	}

	sb.Reset()
	found = srv.exploreDirectory(store, focus, &sb)
	if !found {
		t.Errorf("exploreDirectory should find facts for focus %q", focus)
	}
	output = sb.String()
	if !strings.Contains(output, "pricing.Service") {
		t.Errorf("should contain pricing.Service, got:\n%s", output)
	}

	// Normalize a specific file path
	focus = srv.normalizeToRelative("/Users/me/development/go-service/lib/pricing_service.go")
	if focus != "lib/pricing_service.go" {
		t.Errorf("normalized file = %q, want lib/pricing_service.go", focus)
	}

	sb.Reset()
	found = srv.exploreFile(store, focus, 1, &sb)
	if !found {
		t.Errorf("exploreFile should find facts for focus %q", focus)
	}
}

// TestScenario_FirstRepoNoAppendThenAppend simulates the exact reported issue:
// 1. generate_snapshot(repo_path="/path/ruby-monolith") — no append, facts have Repo: ""
// 2. generate_snapshot(repo_path="/path/go-service", append=true)
// 3. query_facts(repo: "ruby-monolith") should return results (retroactively tagged)
func TestScenario_FirstRepoNoAppendThenAppend(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/development/ruby-monolith")
	srv := &Server{eng: eng}

	store := eng.Store()

	// Step 1: Simulate facts from "ruby-monolith" (the first non-append snapshot).
	// These facts have Repo: "" and unprefixed file paths — exactly like
	// what GenerateSnapshot produces without append.
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "Core", File: "lib/core.rb",
			Props: map[string]any{"language": "ruby"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "CoreUtils", File: "lib/utils.rb", Line: 10,
			Props: map[string]any{"symbol_kind": "class", "exported": true, "language": "ruby"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "CoreLogger", File: "lib/logger.rb", Line: 1,
			Props: map[string]any{"symbol_kind": "class", "exported": true, "language": "ruby"}},
	)

	// In the new flow, SetRepoRange is called right after extraction even in
	// non-append mode, so Repo is already set.
	store.SetRepoRange(0, "ruby-monolith")

	// Verify: repo filter works immediately (before any append)
	results, total := store.QueryAdvanced(facts.QueryOpts{Repo: "ruby-monolith"})
	if total != 3 {
		t.Errorf("before append: repo=ruby-monolith should return 3, got %d", total)
	}

	// Step 2: Simulate entering append mode.
	// TagUntagged retroactively prefixes file paths for facts that already
	// have Repo set but lack the file prefix.
	prevLabel := "ruby-monolith" // filepath.Base("/Users/me/development/ruby-monolith")
	tagged := store.TagUntagged(prevLabel, prevLabel+"/")
	t.Logf("retroactively prefixed %d facts with file prefix %q", tagged, prevLabel+"/")

	if tagged != 3 {
		t.Errorf("expected 3 file paths prefixed, got %d", tagged)
	}

	// Now add go-service facts (simulating what TagRange does)
	preCount := store.Count()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "PricingService", File: "lib/pricing_service.rb", Line: 5,
			Props:     map[string]any{"symbol_kind": "class", "exported": true, "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "CoreUtils"}}},
	)
	store.TagRange(preCount, "go-service", "go-service/")

	// Step 3: Verify both repos are now queryable

	// repo: "ruby-monolith" should now return results
	results, total = store.QueryAdvanced(facts.QueryOpts{Repo: "ruby-monolith"})
	if total != 3 {
		t.Errorf("repo=ruby-monolith: total=%d, want 3", total)
	}
	for _, f := range results {
		if f.Repo != "ruby-monolith" {
			t.Errorf("expected Repo=ruby-monolith, got %q for %s", f.Repo, f.Name)
		}
	}

	// repo: "go-service" should return results
	results, total = store.QueryAdvanced(facts.QueryOpts{Repo: "go-service"})
	if total != 1 {
		t.Errorf("repo=go-service: total=%d, want 1", total)
	}

	// file_prefix: "ruby-monolith" should work (files are now ruby-monolith/lib/...)
	results, total = store.QueryAdvanced(facts.QueryOpts{FilePrefix: "ruby-monolith/"})
	if total != 3 {
		t.Errorf("file_prefix=ruby-monolith/: total=%d, want 3", total)
	}

	// file_prefix: "go-service" should work
	results, total = store.QueryAdvanced(facts.QueryOpts{FilePrefix: "go-service/"})
	if total != 1 {
		t.Errorf("file_prefix=go-service/: total=%d, want 1", total)
	}

	// explore with absolute path to ruby-monolith should resolve
	focus := srv.normalizeToRelative("/Users/me/development/ruby-monolith")
	t.Logf("normalized focus for ruby-monolith: %q", focus)

	// In multi-repo mode, normalizeToRelative should find ruby-monolith in repoPaths
	eng.SetRepoPaths(map[string]string{
		"ruby-monolith": "/Users/me/development/ruby-monolith",
		"go-service":    "/Users/me/development/go-service",
	})
	focus = srv.normalizeToRelative("/Users/me/development/ruby-monolith")
	t.Logf("normalized focus for ruby-monolith (with repoPaths): %q", focus)
	if focus != "ruby-monolith" {
		t.Errorf("expected focus=ruby-monolith, got %q", focus)
	}

	var sb strings.Builder
	found := srv.exploreDirectory(store, focus, &sb)
	if !found {
		t.Errorf("exploreDirectory should find facts for focus %q", focus)
	}
	output := sb.String()
	if !strings.Contains(output, "CoreUtils") {
		t.Errorf("explore output should contain CoreUtils, got:\n%s", output)
	}
	if !strings.Contains(output, "CoreLogger") {
		t.Errorf("explore output should contain CoreLogger, got:\n%s", output)
	}

	_ = results // avoid unused
}

// --- exploreModuleSubstring tests ---

func TestExploreModuleSubstring_SingleMatch(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	// "server" should substring-match "internal/server" (the only module containing "server")
	found := srv.exploreModuleSubstring(store, "server", 1, &sb)
	if !found {
		t.Fatal("exploreModuleSubstring should find a module matching 'server'")
	}

	output := sb.String()
	// Single match delegates to full exploreModule rendering
	if !strings.Contains(output, "# Module: internal/server") {
		t.Errorf("expected full module exploration, got:\n%s", output)
	}
}

func TestExploreModuleSubstring_MultipleMatches(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	// "internal" should substring-match both "internal/server" and "internal/facts"
	found := srv.exploreModuleSubstring(store, "internal", 1, &sb)
	if !found {
		t.Fatal("exploreModuleSubstring should find modules matching 'internal'")
	}

	output := sb.String()
	if !strings.Contains(output, "Multiple modules matching") {
		t.Errorf("expected disambiguation list, got:\n%s", output)
	}
	if !strings.Contains(output, "internal/server") {
		t.Error("should list internal/server")
	}
	if !strings.Contains(output, "internal/facts") {
		t.Error("should list internal/facts")
	}
}

func TestExploreModuleSubstring_NoMatch(t *testing.T) {
	store := populateTestStore()
	srv := newTestServer(store)

	var sb strings.Builder
	found := srv.exploreModuleSubstring(store, "nonexistent", 1, &sb)
	if found {
		t.Error("exploreModuleSubstring should return false for nonexistent")
	}
}

// --- expandFilePrefix tests ---

func TestExpandFilePrefix_SingleRepo(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/development/myrepo")
	srv := &Server{eng: eng}

	// No repoPaths set — single repo mode. Should pass through.
	prefixes := srv.expandFilePrefix("src/")
	if len(prefixes) != 1 || prefixes[0] != "src/" {
		t.Errorf("single-repo: expected [src/], got %v", prefixes)
	}
}

func TestExpandFilePrefix_MultiRepoExpands(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/workspace")
	eng.SetRepoPaths(map[string]string{
		"golf-ui": "/Users/me/development/golf-ui",
		"golf":    "/Users/me/development/golf",
	})
	srv := &Server{eng: eng}

	store := eng.Store()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "AuthForm", File: "golf-ui/src/components/Auth.tsx", Repo: "golf-ui"},
		facts.Fact{Kind: facts.KindSymbol, Name: "LoginPage", File: "golf-ui/src/pages/login.tsx", Repo: "golf-ui"},
		facts.Fact{Kind: facts.KindModule, Name: "internal/auth", File: "golf/internal/auth/auth.go", Repo: "golf"},
	)

	// "src/" doesn't start with a repo label — should expand to "golf-ui/src/"
	prefixes := srv.expandFilePrefix("src/")
	if len(prefixes) != 1 || prefixes[0] != "golf-ui/src/" {
		t.Errorf("expected [golf-ui/src/], got %v", prefixes)
	}

	// "internal/" should expand to "golf/internal/"
	prefixes = srv.expandFilePrefix("internal/")
	if len(prefixes) != 1 || prefixes[0] != "golf/internal/" {
		t.Errorf("expected [golf/internal/], got %v", prefixes)
	}
}

func TestExpandFilePrefix_AlreadyPrefixed(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/workspace")
	eng.SetRepoPaths(map[string]string{
		"golf-ui": "/Users/me/development/golf-ui",
	})
	srv := &Server{eng: eng}

	store := eng.Store()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "AuthForm", File: "golf-ui/src/Auth.tsx", Repo: "golf-ui"},
	)

	// Already prefixed — should pass through unchanged.
	prefixes := srv.expandFilePrefix("golf-ui/src/")
	if len(prefixes) != 1 || prefixes[0] != "golf-ui/src/" {
		t.Errorf("already-prefixed: expected [golf-ui/src/], got %v", prefixes)
	}
}

func TestExpandFilePrefix_Empty(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/workspace")
	srv := &Server{eng: eng}

	prefixes := srv.expandFilePrefix("")
	if len(prefixes) != 1 || prefixes[0] != "" {
		t.Errorf("empty: expected [\"\"], got %v", prefixes)
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"module", "Module"},
		{"symbol", "Symbol"},
		{"", ""},
		{"A", "A"},
	}
	for _, tt := range tests {
		if got := capitalize(tt.input); got != tt.want {
			t.Errorf("capitalize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
