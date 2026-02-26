package facts

// Tests that target the four memory/performance improvements made in the
// graph memory management pass:
//
//  1. edgeSeen is nil'd after NewGraph (memory release)
//  2. BFS uses an index pointer instead of queue re-slicing (no backing-array leak)
//  3. QueryAdvanced/Query use index-bounded candidate sets (O(K) instead of O(N))
//  4. ReverseLookup delegates to Graph.reverse when available (O(1) vs O(N×R))

import (
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// 1. edgeSeen freed after graph construction
// ---------------------------------------------------------------------------

func TestNewGraph_EdgeSeenNilAfterConstruction(t *testing.T) {
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "X", File: "x.go", Relations: []Relation{
			{Kind: RelCalls, Target: "Y"},
		}},
		Fact{Kind: KindSymbol, Name: "Y", File: "y.go"},
	)
	s.BuildGraph()
	g := s.Graph()
	if g == nil {
		t.Fatal("graph should not be nil after BuildGraph")
	}
	// edgeSeen must be nil so its backing memory can be GC'd.
	if g.edgeSeen != nil {
		t.Errorf("edgeSeen should be nil after NewGraph returns, got map of len %d", len(g.edgeSeen))
	}
}

// Deduplication must still work even though edgeSeen is nil'd at the end.
func TestNewGraph_EdgeSeenNilDoesNotBreakDedup(t *testing.T) {
	s := NewStore()
	// Two facts that would produce the same A→imports→B edge.
	s.Add(
		Fact{Kind: KindModule, Name: "A", File: "a.go", Relations: []Relation{
			{Kind: RelImports, Target: "B"},
		}},
		Fact{Kind: KindDependency, Name: "A -> B", File: "a.go", Relations: []Relation{
			{Kind: RelImports, Target: "B"},
		}},
		Fact{Kind: KindModule, Name: "B", File: "b.go"},
	)
	s.BuildGraph()
	g := s.Graph()

	fwd := g.Forward()
	count := 0
	for _, e := range fwd["A"] {
		if e.RelKind == RelImports && e.Target == "B" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("A→imports→B should appear exactly once, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// 2. BFS correctness with the index-pointer queue
// ---------------------------------------------------------------------------

// buildWideGraph creates a hub-and-spoke topology that exercises many queue
// insertions in a single BFS level:
//
//	hub → spoke0, spoke1, …, spoke(n-1)
//	each spoke → leaf
func buildWideGraph(n int) *Store {
	s := NewStore()
	hubRels := make([]Relation, n)
	for i := 0; i < n; i++ {
		hubRels[i] = Relation{Kind: RelCalls, Target: fmt.Sprintf("spoke%d", i)}
	}
	s.Add(Fact{Kind: KindModule, Name: "hub", File: "hub.go", Relations: hubRels})
	for i := 0; i < n; i++ {
		spoke := fmt.Sprintf("spoke%d", i)
		leaf := fmt.Sprintf("leaf%d", i)
		s.Add(Fact{Kind: KindSymbol, Name: spoke, File: spoke + ".go", Relations: []Relation{
			{Kind: RelCalls, Target: leaf},
		}})
		s.Add(Fact{Kind: KindSymbol, Name: leaf, File: leaf + ".go"})
	}
	s.BuildGraph()
	return s
}

func TestTraverse_WideGraphIndexPointerQueue(t *testing.T) {
	const spokes = 50
	s := buildWideGraph(spokes)
	g := s.Graph()

	result := g.Traverse("hub", "forward", nil, nil, 20, 500)

	// hub + spokes + leaves
	want := 1 + spokes + spokes
	if result.Stats.NodesVisited != want {
		t.Errorf("NodesVisited = %d, want %d", result.Stats.NodesVisited, want)
	}
	if result.Stats.Truncated {
		t.Error("result should not be truncated with maxNodes=500")
	}
}

func TestTraverse_BFSDepthOrderIsCorrect(t *testing.T) {
	// Linear chain: A → B → C → D
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "A", Relations: []Relation{{Kind: RelCalls, Target: "B"}}},
		Fact{Kind: KindSymbol, Name: "B", Relations: []Relation{{Kind: RelCalls, Target: "C"}}},
		Fact{Kind: KindSymbol, Name: "C", Relations: []Relation{{Kind: RelCalls, Target: "D"}}},
		Fact{Kind: KindSymbol, Name: "D"},
	)
	s.BuildGraph()

	result := s.Graph().Traverse("A", "forward", nil, nil, 10, 100)

	// Nodes should appear in BFS order: A(0), B(1), C(2), D(3)
	depthOf := make(map[string]int)
	for _, n := range result.Nodes {
		depthOf[n.Name] = n.Depth
	}

	expected := map[string]int{"A": 0, "B": 1, "C": 2, "D": 3}
	for name, wantDepth := range expected {
		if got, ok := depthOf[name]; !ok {
			t.Errorf("node %q missing from result", name)
		} else if got != wantDepth {
			t.Errorf("node %q depth = %d, want %d", name, got, wantDepth)
		}
	}
}

func TestFindPath_IndexPointerQueueCorrectness(t *testing.T) {
	// Use a wide graph; FindPath must still find the two-hop path.
	const spokes = 20
	s := buildWideGraph(spokes)
	g := s.Graph()

	result := g.FindPath("hub", "leaf10", nil, 10)
	if !result.Found {
		t.Fatal("path hub → spoke10 → leaf10 should be found")
	}
	if len(result.Path) != 3 {
		t.Errorf("path length = %d, want 3 (hub, spoke10, leaf10); path = %v", len(result.Path), pathNames(result.Path))
	}
}

// ---------------------------------------------------------------------------
// 3. QueryAdvanced / Query index-acceleration
// ---------------------------------------------------------------------------

// buildLargeStore creates a store with nSymbols symbols and nModules modules
// plus a single dependency fact, all in distinct files.
func buildLargeStore(nSymbols, nModules int) *Store {
	s := NewStore()
	for i := 0; i < nSymbols; i++ {
		s.Add(Fact{Kind: KindSymbol, Name: fmt.Sprintf("sym%d", i), File: fmt.Sprintf("sym%d.go", i)})
	}
	for i := 0; i < nModules; i++ {
		s.Add(Fact{Kind: KindModule, Name: fmt.Sprintf("mod%d", i), File: fmt.Sprintf("mod%d.go", i)})
	}
	s.Add(Fact{Kind: KindDependency, Name: "dep0", File: "dep0.go"})
	return s
}

// TestQueryAdvanced_SingleKindIndexPath verifies that the kind-index fast path
// returns exactly the right facts and that cross-kind results are excluded.
func TestQueryAdvanced_SingleKindIndexPath(t *testing.T) {
	s := buildLargeStore(100, 10)

	results, total := s.QueryAdvanced(QueryOpts{Kind: KindModule})
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	for _, f := range results {
		if f.Kind != KindModule {
			t.Errorf("expected only module facts, got kind=%q", f.Kind)
		}
	}
}

// TestQueryAdvanced_NonexistentKindFastExit verifies that querying for a kind
// that has no facts returns immediately with 0 results.
func TestQueryAdvanced_NonexistentKindFastExit(t *testing.T) {
	s := buildLargeStore(50, 5)

	results, total := s.QueryAdvanced(QueryOpts{Kind: KindRoute})
	if total != 0 {
		t.Errorf("total = %d, want 0 for nonexistent kind", total)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0 for nonexistent kind", len(results))
	}
}

// TestQueryAdvanced_SingleFileIndexPath verifies that the file-index fast path
// is used when exactly one file is specified and no kind filter is active.
func TestQueryAdvanced_SingleFileIndexPath(t *testing.T) {
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "Foo", File: "target.go"},
		Fact{Kind: KindModule, Name: "Bar", File: "target.go"},
		Fact{Kind: KindSymbol, Name: "Baz", File: "other.go"},
	)

	results, total := s.QueryAdvanced(QueryOpts{File: "target.go"})
	if total != 2 {
		t.Errorf("total = %d, want 2 (Foo + Bar in target.go)", total)
	}
	for _, f := range results {
		if f.File != "target.go" {
			t.Errorf("expected only target.go facts, got file=%q", f.File)
		}
	}
}

// TestQueryAdvanced_SingleFileNonexistent verifies fast exit for a missing file.
func TestQueryAdvanced_SingleFileNonexistent(t *testing.T) {
	s := buildLargeStore(20, 5)

	results, total := s.QueryAdvanced(QueryOpts{File: "does_not_exist.go"})
	if total != 0 || len(results) != 0 {
		t.Errorf("expected 0 results for nonexistent file, got total=%d results=%d", total, len(results))
	}
}

// TestQueryAdvanced_NameBatchIndexPath verifies that the exact-names union path
// correctly restricts the candidate set without losing any matches.
func TestQueryAdvanced_NameBatchIndexPath(t *testing.T) {
	s := buildLargeStore(200, 0)

	target := []string{"sym10", "sym20", "sym30"}
	results, total := s.QueryAdvanced(QueryOpts{Names: target, Limit: 500})
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	got := make(map[string]bool, 3)
	for _, f := range results {
		got[f.Name] = true
	}
	for _, name := range target {
		if !got[name] {
			t.Errorf("expected %q in results but it was missing", name)
		}
	}
}

// TestQueryAdvanced_IndexPathWithAdditionalFilters confirms that secondary
// filters (relKind, repo, etc.) are still applied over the index-bounded set.
func TestQueryAdvanced_IndexPathWithAdditionalFilters(t *testing.T) {
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "A", File: "a.go", Repo: "myrepo",
			Relations: []Relation{{Kind: RelCalls, Target: "B"}}},
		Fact{Kind: KindSymbol, Name: "B", File: "b.go", Repo: "myrepo"},
		Fact{Kind: KindSymbol, Name: "C", File: "c.go", Repo: "other"},
	)

	// Kind index path + repo filter should apply both
	results, total := s.QueryAdvanced(QueryOpts{Kind: KindSymbol, Repo: "myrepo"})
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	for _, f := range results {
		if f.Repo != "myrepo" {
			t.Errorf("expected only myrepo facts, got repo=%q", f.Repo)
		}
	}

	// Kind index path + relKind filter
	results, total = s.QueryAdvanced(QueryOpts{Kind: KindSymbol, RelKind: RelCalls})
	if total != 1 {
		t.Errorf("total = %d, want 1 (only A has calls relation)", total)
	}
	if len(results) > 0 && results[0].Name != "A" {
		t.Errorf("expected A, got %q", results[0].Name)
	}
}

// TestQuery_KindIndexFastPath verifies Query's kind-indexed path returns the
// same results as a full-scan would.
func TestQuery_KindIndexFastPath(t *testing.T) {
	s := buildLargeStore(100, 20)

	got := s.Query(KindModule, "", "", "")
	if len(got) != 20 {
		t.Errorf("Query(module) = %d, want 20", len(got))
	}
	for _, f := range got {
		if f.Kind != KindModule {
			t.Errorf("Query(module) returned kind=%q", f.Kind)
		}
	}
}

// TestQuery_KindIndexFastPath_WithFileFilter confirms that the file filter is
// applied correctly over the index-bounded candidate set in Query.
func TestQuery_KindIndexFastPath_WithFileFilter(t *testing.T) {
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "A", File: "target.go"},
		Fact{Kind: KindSymbol, Name: "B", File: "other.go"},
		Fact{Kind: KindModule, Name: "Mod", File: "target.go"},
	)

	// kind=symbol, file=target.go → only A
	got := s.Query(KindSymbol, "target.go", "", "")
	if len(got) != 1 || got[0].Name != "A" {
		t.Errorf("Query(symbol, target.go) = %v, want [A]", got)
	}
}

// TestQuery_NonexistentKindReturnsNil verifies the fast-exit for missing kinds.
func TestQuery_NonexistentKindReturnsNil(t *testing.T) {
	s := buildLargeStore(50, 10)

	got := s.Query(KindRoute, "", "", "")
	if len(got) != 0 {
		t.Errorf("Query(route) = %d, want 0 for nonexistent kind", len(got))
	}
}

// ---------------------------------------------------------------------------
// 4. ReverseLookup graph delegation
// ---------------------------------------------------------------------------

func buildReverseLookupStore() *Store {
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "A", File: "a.go", Relations: []Relation{
			{Kind: RelCalls, Target: "C"},
			{Kind: RelImports, Target: "C"},
		}},
		Fact{Kind: KindSymbol, Name: "B", File: "b.go", Relations: []Relation{
			{Kind: RelCalls, Target: "C"},
		}},
		Fact{Kind: KindDependency, Name: "dep", File: "a.go", Relations: []Relation{
			{Kind: RelImports, Target: "C"},
		}},
		Fact{Kind: KindSymbol, Name: "C", File: "c.go"},
		Fact{Kind: KindSymbol, Name: "Z", File: "z.go"}, // no relations targeting anything
	)
	return s
}

// TestReverseLookup_FallbackWithoutGraph verifies the linear-scan path
// (graph not yet built).
func TestReverseLookup_FallbackWithoutGraph(t *testing.T) {
	s := buildReverseLookupStore()

	// Graph not built — should fall back to linear scan.
	if s.Graph() != nil {
		t.Fatal("graph should be nil (BuildGraph not called)")
	}

	callers := s.ReverseLookup("C", RelCalls)
	if len(callers) != 2 {
		t.Errorf("callers of C (calls) = %d, want 2 (A and B)", len(callers))
	}
	names := factNames(callers)
	if !contains(names, "A") || !contains(names, "B") {
		t.Errorf("callers should be A and B, got %v", names)
	}

	importers := s.ReverseLookup("C", RelImports)
	if len(importers) != 2 {
		t.Errorf("importers of C = %d, want 2 (A and dep)", len(importers))
	}

	all := s.ReverseLookup("C", "")
	// A (calls+imports counted once), B (calls), dep (imports) = 3
	if len(all) != 3 {
		t.Errorf("all reverse of C = %d, want 3", len(all))
	}

	none := s.ReverseLookup("Z", "")
	if len(none) != 0 {
		t.Errorf("reverse of Z = %d, want 0", len(none))
	}
}

// TestReverseLookup_GraphFastPath verifies the graph-index path
// (graph is built) returns the same correct results.
func TestReverseLookup_GraphFastPath(t *testing.T) {
	s := buildReverseLookupStore()
	s.BuildGraph()

	if s.Graph() == nil {
		t.Fatal("graph should be non-nil after BuildGraph")
	}

	callers := s.ReverseLookup("C", RelCalls)
	if len(callers) != 2 {
		t.Errorf("callers of C (calls) = %d, want 2 (A and B)", len(callers))
	}
	names := factNames(callers)
	if !contains(names, "A") || !contains(names, "B") {
		t.Errorf("callers should be A and B, got %v", names)
	}

	none := s.ReverseLookup("Z", "")
	if len(none) != 0 {
		t.Errorf("reverse of Z = %d, want 0", len(none))
	}
}

// TestReverseLookup_BothPathsAgree builds the same store twice — once
// without a graph (linear scan) and once with (fast path) — and asserts
// that both return identical result sets.
func TestReverseLookup_BothPathsAgree(t *testing.T) {
	targets := []string{"C", "Z", "A"}
	relKinds := []string{RelCalls, RelImports, ""}

	for _, target := range targets {
		for _, relKind := range relKinds {
			// Without graph (fallback)
			s1 := buildReverseLookupStore()
			scanResult := s1.ReverseLookup(target, relKind)

			// With graph (fast path)
			s2 := buildReverseLookupStore()
			s2.BuildGraph()
			graphResult := s2.ReverseLookup(target, relKind)

			if len(scanResult) != len(graphResult) {
				t.Errorf("ReverseLookup(%q, %q): scan=%d graph=%d (counts differ)",
					target, relKind, len(scanResult), len(graphResult))
				continue
			}

			scanNames := factNames(scanResult)
			graphNames := factNames(graphResult)
			for _, n := range scanNames {
				if !contains(graphNames, n) {
					t.Errorf("ReverseLookup(%q, %q): scan has %q but graph path does not; graph=%v",
						target, relKind, n, graphNames)
				}
			}
		}
	}
}

// TestReverseLookup_GraphFastPath_AfterClearAndRebuild verifies that the
// fast path remains correct after the store is cleared and the graph rebuilt.
func TestReverseLookup_GraphFastPath_AfterClearAndRebuild(t *testing.T) {
	s := buildReverseLookupStore()
	s.BuildGraph()

	s.Clear()

	// After Clear, graph is nil — must fall back to scan (which returns nothing).
	callers := s.ReverseLookup("C", RelCalls)
	if len(callers) != 0 {
		t.Errorf("after Clear: expected 0 callers, got %d", len(callers))
	}

	// Re-add and rebuild.
	s.Add(
		Fact{Kind: KindSymbol, Name: "X", File: "x.go", Relations: []Relation{
			{Kind: RelCalls, Target: "Y"},
		}},
		Fact{Kind: KindSymbol, Name: "Y", File: "y.go"},
	)
	s.BuildGraph()

	callers = s.ReverseLookup("Y", RelCalls)
	if len(callers) != 1 || callers[0].Name != "X" {
		t.Errorf("after rebuild: callers of Y = %v, want [X]", factNames(callers))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func factNames(ff []Fact) []string {
	names := make([]string, len(ff))
	for i, f := range ff {
		names[i] = f.Name
	}
	return names
}
