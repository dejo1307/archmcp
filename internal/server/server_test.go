package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
