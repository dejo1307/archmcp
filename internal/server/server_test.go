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
		eng: newEngineWithSnapshot("/Users/me/vinted"),
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"absolute subdir", "/Users/me/vinted/svc-pricing", "svc-pricing"},
		{"absolute file", "/Users/me/vinted/svc-pricing/lib/foo.rb", "svc-pricing/lib/foo.rb"},
		{"absolute repo root", "/Users/me/vinted", "."},
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
		"svc-pricing": "/Users/me/vinted/svc-pricing",
		"core":        "/Users/me/vinted/core",
	})
	srv := &Server{eng: eng}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"multi-repo svc-pricing dir", "/Users/me/vinted/svc-pricing", "svc-pricing"},
		{"multi-repo svc-pricing file", "/Users/me/vinted/svc-pricing/lib/foo.rb", "svc-pricing/lib/foo.rb"},
		{"multi-repo core file", "/Users/me/vinted/core/lib/bar.rb", "core/lib/bar.rb"},
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
// query_facts with file_prefix like "svc-pricing/..." or "core/..." returned nothing.
func TestScenario_QueryFactsWithFilePrefixCrossRepo(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/vinted/workspace")
	eng.SetRepoPaths(map[string]string{
		"svc-pricing": "/Users/me/vinted/svc-pricing",
		"core":        "/Users/me/vinted/core",
	})
	srv := &Server{eng: eng}

	store := eng.Store()

	// Simulate repo A facts (svc-pricing) - files prefixed as in append mode
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "Pricing", File: "svc-pricing/lib/pricing.rb", Repo: "svc-pricing",
			Props: map[string]any{"language": "ruby"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "PricingService", File: "svc-pricing/lib/pricing_service.rb", Line: 5, Repo: "svc-pricing",
			Props: map[string]any{"symbol_kind": "class", "exported": true, "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "CoreUtils"}}},
		facts.Fact{Kind: facts.KindDependency, Name: "svc-pricing -> core", File: "svc-pricing/lib/pricing_service.rb", Repo: "svc-pricing",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "core"}}},
	)

	// Simulate repo B facts (core)
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "Core", File: "core/lib/core.rb", Repo: "core",
			Props: map[string]any{"language": "ruby"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "CoreUtils", File: "core/lib/utils.rb", Line: 10, Repo: "core",
			Props: map[string]any{"symbol_kind": "class", "exported": true, "language": "ruby"}},
	)

	// Test 1: query_facts with file_prefix "svc-pricing" should find svc-pricing facts
	results, total := store.QueryAdvanced(facts.QueryOpts{FilePrefix: "svc-pricing"})
	if total != 3 {
		t.Errorf("file_prefix=svc-pricing: total=%d, want 3", total)
	}
	for _, f := range results {
		if !strings.HasPrefix(f.File, "svc-pricing") {
			t.Errorf("unexpected file %q in svc-pricing results", f.File)
		}
	}

	// Test 2: query_facts with file_prefix "core" should find core facts
	results, total = store.QueryAdvanced(facts.QueryOpts{FilePrefix: "core"})
	if total != 2 {
		t.Errorf("file_prefix=core: total=%d, want 2", total)
	}

	// Test 3: repo filter should work
	results, total = store.QueryAdvanced(facts.QueryOpts{Repo: "svc-pricing"})
	if total != 3 {
		t.Errorf("repo=svc-pricing: total=%d, want 3", total)
	}

	// Test 4: normalize absolute path to file_prefix
	normalized := srv.normalizeToRelative("/Users/me/vinted/svc-pricing/lib")
	if normalized != "svc-pricing/lib" {
		t.Errorf("normalize(/Users/me/vinted/svc-pricing/lib) = %q, want svc-pricing/lib", normalized)
	}
	results, total = store.QueryAdvanced(facts.QueryOpts{FilePrefix: normalized})
	if total != 3 {
		t.Errorf("normalized file_prefix: total=%d, want 3 (module + symbol + dep all in svc-pricing/lib/)", total)
	}
}

// TestScenario_ExploreWithAbsolutePathCrossRepo simulates the second reported issue:
// explore with focus "/Users/.../svc-pricing" returned "No facts matching focus".
func TestScenario_ExploreWithAbsolutePathCrossRepo(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/workspace")
	eng.SetRepoPaths(map[string]string{
		"svc-pricing": "/Users/me/vinted/svc-pricing",
		"core":        "/Users/me/vinted/core",
	})
	srv := &Server{eng: eng}

	store := eng.Store()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "PricingService", File: "svc-pricing/lib/pricing_service.rb", Line: 5, Repo: "svc-pricing",
			Props: map[string]any{"symbol_kind": "class", "exported": true}},
		facts.Fact{Kind: facts.KindSymbol, Name: "CoreUtils", File: "core/lib/utils.rb", Line: 10, Repo: "core",
			Props: map[string]any{"symbol_kind": "class", "exported": true}},
	)

	// Test: explore with absolute path to svc-pricing repo root
	focus := srv.normalizeToRelative("/Users/me/vinted/svc-pricing")
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

	// Test: explore with absolute path to core repo root
	focus = srv.normalizeToRelative("/Users/me/vinted/core")
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
	focus = srv.normalizeToRelative("/Users/me/vinted/svc-pricing/lib")
	if focus != "svc-pricing/lib" {
		t.Errorf("normalized subdir = %q, want svc-pricing/lib", focus)
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
	eng := newEngineWithSnapshot("/Users/me/vinted/svc-pricing")
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
	focus := srv.normalizeToRelative("/Users/me/vinted/svc-pricing")
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
	focus = srv.normalizeToRelative("/Users/me/vinted/svc-pricing/lib")
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
	focus = srv.normalizeToRelative("/Users/me/vinted/svc-pricing/lib/pricing_service.go")
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
// 1. generate_snapshot(repo_path="/path/core") — no append, facts have Repo: ""
// 2. generate_snapshot(repo_path="/path/svc-pricing", append=true)
// 3. query_facts(repo: "core") should return results (retroactively tagged)
func TestScenario_FirstRepoNoAppendThenAppend(t *testing.T) {
	eng := newEngineWithSnapshot("/Users/me/vinted/core")
	srv := &Server{eng: eng}

	store := eng.Store()

	// Step 1: Simulate facts from "core" (the first non-append snapshot).
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
	store.SetRepoRange(0, "core")

	// Verify: repo filter works immediately (before any append)
	results, total := store.QueryAdvanced(facts.QueryOpts{Repo: "core"})
	if total != 3 {
		t.Errorf("before append: repo=core should return 3, got %d", total)
	}

	// Step 2: Simulate entering append mode.
	// TagUntagged retroactively prefixes file paths for facts that already
	// have Repo set but lack the file prefix.
	prevLabel := "core" // filepath.Base("/Users/me/vinted/core")
	tagged := store.TagUntagged(prevLabel, prevLabel+"/")
	t.Logf("retroactively prefixed %d facts with file prefix %q", tagged, prevLabel+"/")

	if tagged != 3 {
		t.Errorf("expected 3 file paths prefixed, got %d", tagged)
	}

	// Now add svc-pricing facts (simulating what TagRange does)
	preCount := store.Count()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "PricingService", File: "lib/pricing_service.rb", Line: 5,
			Props: map[string]any{"symbol_kind": "class", "exported": true, "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "CoreUtils"}}},
	)
	store.TagRange(preCount, "svc-pricing", "svc-pricing/")

	// Step 3: Verify both repos are now queryable

	// repo: "core" should now return results
	results, total = store.QueryAdvanced(facts.QueryOpts{Repo: "core"})
	if total != 3 {
		t.Errorf("repo=core: total=%d, want 3", total)
	}
	for _, f := range results {
		if f.Repo != "core" {
			t.Errorf("expected Repo=core, got %q for %s", f.Repo, f.Name)
		}
	}

	// repo: "svc-pricing" should return results
	results, total = store.QueryAdvanced(facts.QueryOpts{Repo: "svc-pricing"})
	if total != 1 {
		t.Errorf("repo=svc-pricing: total=%d, want 1", total)
	}

	// file_prefix: "core" should work (files are now core/lib/...)
	results, total = store.QueryAdvanced(facts.QueryOpts{FilePrefix: "core/"})
	if total != 3 {
		t.Errorf("file_prefix=core/: total=%d, want 3", total)
	}

	// file_prefix: "svc-pricing" should work
	results, total = store.QueryAdvanced(facts.QueryOpts{FilePrefix: "svc-pricing/"})
	if total != 1 {
		t.Errorf("file_prefix=svc-pricing/: total=%d, want 1", total)
	}

	// explore with absolute path to core should resolve
	focus := srv.normalizeToRelative("/Users/me/vinted/core")
	t.Logf("normalized focus for core: %q", focus)

	// In multi-repo mode, normalizeToRelative should find core in repoPaths
	eng.SetRepoPaths(map[string]string{
		"core":        "/Users/me/vinted/core",
		"svc-pricing": "/Users/me/vinted/svc-pricing",
	})
	focus = srv.normalizeToRelative("/Users/me/vinted/core")
	t.Logf("normalized focus for core (with repoPaths): %q", focus)
	if focus != "core" {
		t.Errorf("expected focus=core, got %q", focus)
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
