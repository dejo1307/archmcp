package facts

import (
	"testing"
)

// buildTestGraph creates a graph from a set of facts for testing.
// The topology is:
//
//	A --calls--> B --calls--> C --calls--> D
//	A --imports-> E
//	E --calls--> C
//	F (disconnected)
func buildTestGraph() (*Graph, *Store) {
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "A", File: "a.go", Line: 1, Relations: []Relation{
			{Kind: RelCalls, Target: "B"},
			{Kind: RelImports, Target: "E"},
		}},
		Fact{Kind: KindSymbol, Name: "B", File: "b.go", Line: 10, Relations: []Relation{
			{Kind: RelCalls, Target: "C"},
		}},
		Fact{Kind: KindModule, Name: "C", File: "c.go", Line: 20, Relations: []Relation{
			{Kind: RelCalls, Target: "D"},
		}},
		Fact{Kind: KindSymbol, Name: "D", File: "d.go", Line: 30},
		Fact{Kind: KindModule, Name: "E", File: "e.go", Line: 40, Relations: []Relation{
			{Kind: RelCalls, Target: "C"},
		}},
		Fact{Kind: KindSymbol, Name: "F", File: "f.go", Line: 50}, // disconnected
	)
	s.BuildGraph()
	return s.Graph(), s
}

// buildCyclicGraph creates a graph with a cycle: A -> B -> C -> A
func buildCyclicGraph() (*Graph, *Store) {
	s := NewStore()
	s.Add(
		Fact{Kind: KindModule, Name: "A", File: "a.go", Relations: []Relation{
			{Kind: RelImports, Target: "B"},
		}},
		Fact{Kind: KindModule, Name: "B", File: "b.go", Relations: []Relation{
			{Kind: RelImports, Target: "C"},
		}},
		Fact{Kind: KindModule, Name: "C", File: "c.go", Relations: []Relation{
			{Kind: RelImports, Target: "A"},
		}},
	)
	s.BuildGraph()
	return s.Graph(), s
}

func TestNewGraph_BuildsAdjacencyLists(t *testing.T) {
	g, _ := buildTestGraph()

	if g.NodeCount() != 6 {
		t.Errorf("NodeCount = %d, want 6", g.NodeCount())
	}
	if g.EdgeCount() != 5 {
		t.Errorf("EdgeCount = %d, want 5", g.EdgeCount())
	}

	// Check forward adjacency for A
	fwd := g.Forward()
	aEdges := fwd["A"]
	if len(aEdges) != 2 {
		t.Errorf("A forward edges = %d, want 2", len(aEdges))
	}

	// Check reverse adjacency for C (B and E both call C)
	rev := g.Reverse()
	cEdges := rev["C"]
	if len(cEdges) != 2 {
		t.Errorf("C reverse edges = %d, want 2", len(cEdges))
	}
}

func TestTraverse_ForwardFromA(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.Traverse("A", "forward", nil, nil, 10, 100)

	// A -> B -> C -> D, A -> E -> C (C already visited)
	// Should visit: A, B, C, D, E
	if result.Stats.NodesVisited != 5 {
		t.Errorf("NodesVisited = %d, want 5", result.Stats.NodesVisited)
	}

	// Nodes in result (including start)
	names := nodeNames(result.Nodes)
	for _, want := range []string{"A", "B", "C", "D", "E"} {
		if !contains(names, want) {
			t.Errorf("missing node %q in traverse result", want)
		}
	}
	if contains(names, "F") {
		t.Error("F should not be reachable from A")
	}
}

func TestTraverse_ReverseFromD(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.Traverse("D", "reverse", nil, nil, 10, 100)

	// D is called by C, C is called by B and E, B is called by A, E is imported by A
	// Reverse from D: D <- C <- B <- A, C <- E <- A (A already visited)
	names := nodeNames(result.Nodes)
	for _, want := range []string{"D", "C", "B", "A", "E"} {
		if !contains(names, want) {
			t.Errorf("missing node %q in reverse traverse from D", want)
		}
	}
}

func TestTraverse_DepthLimit(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.Traverse("A", "forward", nil, nil, 1, 100)

	// Depth 1: A -> B, A -> E (only direct neighbors)
	names := nodeNames(result.Nodes)
	if !contains(names, "A") || !contains(names, "B") || !contains(names, "E") {
		t.Errorf("depth-1 should include A, B, E; got %v", names)
	}
	if contains(names, "C") || contains(names, "D") {
		t.Errorf("depth-1 should NOT include C or D; got %v", names)
	}
}

func TestTraverse_MaxNodesLimit(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.Traverse("A", "forward", nil, nil, 10, 3)

	// Should only return at most 3 nodes
	if len(result.Nodes) > 3 {
		t.Errorf("maxNodes=3 but got %d nodes", len(result.Nodes))
	}
	if !result.Stats.Truncated {
		t.Error("should be truncated with maxNodes=3")
	}
}

func TestTraverse_RelationKindFilter(t *testing.T) {
	g, _ := buildTestGraph()

	// Only follow "calls" relations from A
	result := g.Traverse("A", "forward", []string{RelCalls}, nil, 10, 100)

	names := nodeNames(result.Nodes)
	// A --calls-> B --calls-> C --calls-> D (imports to E skipped)
	for _, want := range []string{"A", "B", "C", "D"} {
		if !contains(names, want) {
			t.Errorf("calls-only traverse missing %q", want)
		}
	}
	if contains(names, "E") {
		t.Error("E should not be reachable via calls-only")
	}
}

func TestTraverse_NodeKindFilter(t *testing.T) {
	g, _ := buildTestGraph()

	// Traverse from A but only include module-kind nodes in results
	// C and E are modules, A/B/D are symbols
	result := g.Traverse("A", "forward", nil, []string{KindModule}, 10, 100)

	names := nodeNames(result.Nodes)
	// Should traverse through symbols but only include modules in result
	// A(sym) -> B(sym) -> C(mod) -> D(sym), A -> E(mod) -> C
	if !contains(names, "C") || !contains(names, "E") {
		t.Errorf("module filter should include C and E; got %v", names)
	}
	// Start node A is always included regardless of filter
	if !contains(names, "A") {
		t.Errorf("start node A should always be included; got %v", names)
	}
}

func TestTraverse_CycleHandling(t *testing.T) {
	g, _ := buildCyclicGraph()

	result := g.Traverse("A", "forward", nil, nil, 20, 100)

	// Should visit A, B, C without infinite loop
	if result.Stats.NodesVisited != 3 {
		t.Errorf("NodesVisited = %d, want 3 (cycle should be handled)", result.Stats.NodesVisited)
	}
	names := nodeNames(result.Nodes)
	for _, want := range []string{"A", "B", "C"} {
		if !contains(names, want) {
			t.Errorf("cycle traverse missing %q", want)
		}
	}
}

func TestTraverse_DisconnectedNode(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.Traverse("F", "forward", nil, nil, 10, 100)

	// F is disconnected, should only return F itself
	if len(result.Nodes) != 1 || result.Nodes[0].Name != "F" {
		t.Errorf("disconnected traverse: got %v, want [F]", nodeNames(result.Nodes))
	}
}

func TestTraverse_NonexistentStart(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.Traverse("NONEXISTENT", "forward", nil, nil, 10, 100)

	// Should still return the start node (with no metadata)
	if len(result.Nodes) != 1 || result.Nodes[0].Name != "NONEXISTENT" {
		t.Errorf("nonexistent start: got %v, want [NONEXISTENT]", nodeNames(result.Nodes))
	}
}

func TestFindPath_DirectConnection(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.FindPath("A", "B", nil, 10)

	if !result.Found {
		t.Fatal("path A->B should be found")
	}
	if len(result.Path) != 2 {
		t.Errorf("path length = %d, want 2 (A, B)", len(result.Path))
	}
	if result.Path[0].Name != "A" || result.Path[1].Name != "B" {
		t.Errorf("path = %v, want [A, B]", pathNames(result.Path))
	}
}

func TestFindPath_MultiHop(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.FindPath("A", "D", nil, 10)

	if !result.Found {
		t.Fatal("path A->D should be found")
	}
	// Shortest: A -> B -> C -> D
	if len(result.Path) != 4 {
		t.Errorf("path length = %d, want 4 (A, B, C, D); path = %v", len(result.Path), pathNames(result.Path))
	}

	// Check edges
	if len(result.Edges) != 3 {
		t.Errorf("edges = %d, want 3", len(result.Edges))
	}
}

func TestFindPath_NoPath(t *testing.T) {
	g, _ := buildTestGraph()

	// F is disconnected
	result := g.FindPath("A", "F", nil, 10)

	if result.Found {
		t.Error("path A->F should not exist")
	}
	if len(result.Path) != 0 {
		t.Errorf("path should be empty, got %v", pathNames(result.Path))
	}
}

func TestFindPath_SameNode(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.FindPath("A", "A", nil, 10)

	if !result.Found {
		t.Fatal("path A->A should be found (trivial)")
	}
	if len(result.Path) != 1 {
		t.Errorf("path length = %d, want 1 (just A)", len(result.Path))
	}
}

func TestFindPath_DepthLimit(t *testing.T) {
	g, _ := buildTestGraph()

	// A -> D needs 3 hops, limit to 2
	result := g.FindPath("A", "D", nil, 2)

	if result.Found {
		t.Error("path A->D should not be found with maxDepth=2")
	}
}

func TestFindPath_RelationKindFilter(t *testing.T) {
	g, _ := buildTestGraph()

	// Only imports: A --imports-> E, but no path from E to D via imports
	result := g.FindPath("A", "D", []string{RelImports}, 10)

	if result.Found {
		t.Error("path A->D via imports only should not exist")
	}
}

func TestFindPath_WithCycle(t *testing.T) {
	g, _ := buildCyclicGraph()

	result := g.FindPath("A", "C", nil, 10)

	if !result.Found {
		t.Fatal("path A->C should be found")
	}
	// Shortest: A -> B -> C
	if len(result.Path) != 3 {
		t.Errorf("path length = %d, want 3; path = %v", len(result.Path), pathNames(result.Path))
	}
}

func TestImpactSet_Basic(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.ImpactSet("C", 10, 100, false)

	if result.Target != "C" {
		t.Errorf("Target = %q, want C", result.Target)
	}

	// Who depends on C? B calls C, E calls C, A calls B and imports E
	// Reverse: C <- B <- A, C <- E <- A (A already counted)
	// Depth 1: B, E
	// Depth 2: A (via B), A (via E, already counted)
	depth1 := result.ByDepth[1]
	depth1Names := make([]string, len(depth1))
	for i, n := range depth1 {
		depth1Names[i] = n.Name
	}
	if len(depth1) != 2 {
		t.Errorf("depth 1 = %d (%v), want 2 (B, E)", len(depth1), depth1Names)
	}
	if !contains(depth1Names, "B") || !contains(depth1Names, "E") {
		t.Errorf("depth 1 should include B and E; got %v", depth1Names)
	}

	depth2 := result.ByDepth[2]
	if len(depth2) != 1 || depth2[0].Name != "A" {
		depth2Names := make([]string, len(depth2))
		for i, n := range depth2 {
			depth2Names[i] = n.Name
		}
		t.Errorf("depth 2 = %v, want [A]", depth2Names)
	}

	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
}

func TestImpactSet_WithForward(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.ImpactSet("C", 10, 100, true)

	if result.Forward == nil {
		t.Fatal("forward dependencies should be included")
	}

	// C -> D (forward)
	names := nodeNames(result.Forward.Nodes)
	if !contains(names, "D") {
		t.Errorf("forward from C should include D; got %v", names)
	}
}

func TestImpactSet_LeafNode(t *testing.T) {
	g, _ := buildTestGraph()

	// D has no dependents
	result := g.ImpactSet("D", 10, 100, false)

	totalDependents := 0
	for _, nodes := range result.ByDepth {
		totalDependents += len(nodes)
	}

	// C calls D, B calls C, E calls C, A calls B and imports E
	if totalDependents < 1 {
		t.Errorf("D should have at least C as direct dependent, got %d total", totalDependents)
	}
}

func TestImpactSet_CycleHandling(t *testing.T) {
	g, _ := buildCyclicGraph()

	result := g.ImpactSet("A", 20, 100, false)

	// In a cycle A->B->C->A, impact of A is: B (depth 1 reverse from A via C->A),
	// Actually reverse: who points TO A? C points to A. Who points to C? B. Who points to B? A (already visited)
	// So: depth 1: C, depth 2: B
	totalDependents := 0
	for _, nodes := range result.ByDepth {
		totalDependents += len(nodes)
	}
	if totalDependents != 2 {
		t.Errorf("cycle impact should have 2 dependents (B, C), got %d", totalDependents)
	}
}

func TestBuildGraph_ViaStore(t *testing.T) {
	s := NewStore()
	s.Add(
		Fact{Kind: KindSymbol, Name: "X", File: "x.go", Relations: []Relation{
			{Kind: RelCalls, Target: "Y"},
		}},
		Fact{Kind: KindSymbol, Name: "Y", File: "y.go"},
	)

	// Before BuildGraph
	if s.Graph() != nil {
		t.Error("Graph should be nil before BuildGraph")
	}

	s.BuildGraph()

	g := s.Graph()
	if g == nil {
		t.Fatal("Graph should not be nil after BuildGraph")
	}
	if g.NodeCount() != 2 {
		t.Errorf("NodeCount = %d, want 2", g.NodeCount())
	}
	if g.EdgeCount() != 1 {
		t.Errorf("EdgeCount = %d, want 1", g.EdgeCount())
	}
}

func TestBuildGraph_ClearedByStoreClear(t *testing.T) {
	s := NewStore()
	s.Add(Fact{Kind: KindSymbol, Name: "X", File: "x.go"})
	s.BuildGraph()
	if s.Graph() == nil {
		t.Fatal("Graph should exist after BuildGraph")
	}

	s.Clear()
	if s.Graph() != nil {
		t.Error("Graph should be nil after Clear")
	}
}

func TestTraverse_DefaultParameters(t *testing.T) {
	g, _ := buildTestGraph()

	// Test with zero values (should use defaults)
	result := g.Traverse("A", "forward", nil, nil, 0, 0)

	// Default maxDepth=5, maxNodes=100
	// Should still find all reachable nodes
	names := nodeNames(result.Nodes)
	if len(names) < 5 {
		t.Errorf("default params should find all reachable nodes; got %d", len(names))
	}
}

func TestFindPath_DefaultMaxDepth(t *testing.T) {
	g, _ := buildTestGraph()

	// maxDepth=0 should use default (10)
	result := g.FindPath("A", "D", nil, 0)

	if !result.Found {
		t.Error("should find path A->D with default maxDepth")
	}
}

func TestTraverse_EdgesAreRecorded(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.Traverse("A", "forward", []string{RelCalls}, nil, 1, 100)

	// A --calls-> B only (depth 1, calls only)
	if len(result.Edges) != 1 {
		t.Errorf("edges = %d, want 1", len(result.Edges))
	}
	if len(result.Edges) > 0 {
		e := result.Edges[0]
		if e.Source != "A" || e.Target != "B" || e.Kind != RelCalls {
			t.Errorf("edge = %+v, want A->B calls", e)
		}
	}
}

func TestFindPath_EdgesHaveCorrectKinds(t *testing.T) {
	g, _ := buildTestGraph()

	result := g.FindPath("A", "C", nil, 10)

	if !result.Found {
		t.Fatal("path should be found")
	}

	// Shortest path A->B->C, edges should have their relation kinds
	for _, e := range result.Edges {
		if e.Kind == "" {
			t.Error("edge kind should not be empty")
		}
	}
}

// --- helpers ---

func nodeNames(nodes []TraversalNode) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func pathNames(nodes []TraversalNode) []string {
	return nodeNames(nodes)
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
