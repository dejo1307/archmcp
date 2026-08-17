package facts

// Differential tests for the CSR traversal layer.
//
// Traversal used to walk map[string][]Edge with a map[string]bool visited set; it now
// walks CSR ranges with a map[uint32]struct{}. The rewrite touched the BFS frontier,
// the relation filter, the node-kind filter, the max-node cap and the edge-consistency
// pass — all at once, and all in ways the existing assertions on small fixtures can
// satisfy by accident.
//
// So the walk is checked against a deliberately naive reference implementation over
// materialized adjacency: same edges, obvious code, no IDs. Where they disagree, the
// reference is right by construction.

import (
	"math/rand"
	"os"
	"reflect"
	"sort"
	"testing"
)

// materializeAdjacency pulls the graph's edges back out into the map-of-slices shape
// the reference walker uses. It reads through the public per-node accessors, so it
// exercises them too.
func materializeAdjacency(g *Graph, reverse bool) map[string][]Edge {
	out := make(map[string][]Edge, len(g.names))
	for _, name := range g.names {
		var edges []Edge
		if reverse {
			edges = g.ReverseEdges(name)
		} else {
			edges = g.ForwardEdges(name)
		}
		if len(edges) > 0 {
			out[name] = edges
		}
	}
	return out
}

// referenceBFS is the naive walk: it returns the names reachable from starts within
// maxDepth, following only relKinds when non-empty. No caps, no filters, no IDs.
func referenceBFS(adj map[string][]Edge, starts []string, relKinds []string, maxDepth int) map[string]int {
	relSet := map[string]bool{}
	for _, r := range relKinds {
		relSet[r] = true
	}
	depth := map[string]int{}
	type item struct {
		name string
		d    int
	}
	var queue []item
	for _, s := range starts {
		if _, seen := depth[s]; !seen {
			depth[s] = 0
			queue = append(queue, item{s, 0})
		}
	}
	for i := 0; i < len(queue); i++ {
		cur := queue[i]
		if cur.d >= maxDepth {
			continue
		}
		for _, e := range adj[cur.name] {
			if len(relSet) > 0 && !relSet[e.RelKind] {
				continue
			}
			if _, seen := depth[e.Target]; seen {
				continue
			}
			depth[e.Target] = cur.d + 1
			queue = append(queue, item{e.Target, cur.d + 1})
		}
	}
	return depth
}

// equivalenceCorpus loads the real fact set when one is configured, and otherwise
// builds a synthetic graph with the awkward shapes real extraction produces:
// self-edges, duplicate relations, dangling targets and name collisions across kinds.
func equivalenceCorpus(t *testing.T) *Store {
	t.Helper()
	if path := os.Getenv("ENOLA_FACTS_CORPUS"); path != "" {
		s := NewStore()
		if err := s.ReadJSONLFile(path); err != nil {
			t.Fatal(err)
		}
		s.BuildGraph()
		return s
	}

	rng := rand.New(rand.NewSource(1))
	s := NewStore()
	const n = 4000
	kinds := []string{RelCalls, RelImports, RelImplements, RelInstantiates, RelDependsOn}
	for i := 0; i < n; i++ {
		name := "pkg" + itoa(i%40) + ".Sym" + itoa(i)
		f := Fact{
			Kind:  KindSymbol,
			Name:  name,
			File:  "pkg" + itoa(i%40) + "/f" + itoa(i%13) + ".go",
			Line:  i,
			Props: map[string]any{"symbol_kind": SymbolFunc},
		}
		for j := 0; j < rng.Intn(6); j++ {
			target := "pkg" + itoa(rng.Intn(40)) + ".Sym" + itoa(rng.Intn(n+500)) // +500 → dangling
			f.Relations = append(f.Relations, Relation{Kind: kinds[rng.Intn(len(kinds))], Target: target})
		}
		if i%37 == 0 {
			f.Relations = append(f.Relations, Relation{Kind: RelCalls, Target: name}) // self-edge
		}
		if len(f.Relations) > 0 {
			f.Relations = append(f.Relations, f.Relations[0]) // exact duplicate
		}
		s.Add(f)
	}
	// Name collisions across kinds: a module and a service sharing a symbol's name.
	for i := 0; i < 40; i++ {
		s.Add(Fact{Kind: KindModule, Name: "pkg" + itoa(i) + ".Sym" + itoa(i), File: "pkg" + itoa(i)})
		s.Add(Fact{Kind: KindService, Name: "pkg" + itoa(i), File: "pkg" + itoa(i)})
	}
	s.BuildGraph()
	return s
}

// sampleNodes picks up to k node names deterministically, biased toward nodes that
// actually have edges — a sample of isolated nodes would prove nothing.
func sampleNodes(g *Graph, k int) []string {
	rng := rand.New(rand.NewSource(42))
	var withEdges []string
	for _, name := range g.names {
		if g.FanOut(name) > 0 || len(g.ReverseEdges(name)) > 0 {
			withEdges = append(withEdges, name)
		}
	}
	if len(withEdges) <= k {
		sort.Strings(withEdges)
		return withEdges
	}
	picked := make([]string, 0, k)
	for i := 0; i < k; i++ {
		picked = append(picked, withEdges[rng.Intn(len(withEdges))])
	}
	sort.Strings(picked)
	return picked
}

// TestTraverse_MatchesReferenceWalk compares the reachable set and per-node depth of
// Traverse against the naive walker, in both directions and with and without a
// relation filter. maxNodes is set high enough that the cap never engages, so this
// isolates the walk from the display cap.
func TestTraverse_MatchesReferenceWalk(t *testing.T) {
	s := equivalenceCorpus(t)
	g := s.Graph()
	fwdAdj := materializeAdjacency(g, false)
	revAdj := materializeAdjacency(g, true)

	cases := []struct {
		direction string
		relKinds  []string
	}{
		{"forward", nil},
		{"reverse", nil},
		{"forward", []string{RelCalls}},
		{"reverse", []string{RelCalls, RelImports}},
		{"forward", []string{"no-such-relation"}},
	}

	for _, start := range sampleNodes(g, 60) {
		for _, tc := range cases {
			adj := fwdAdj
			if tc.direction == "reverse" {
				adj = revAdj
			}
			const maxDepth = 4
			want := referenceBFS(adj, []string{start}, tc.relKinds, maxDepth)

			got := g.Traverse(start, tc.direction, tc.relKinds, nil, maxDepth, 500)
			if got.Stats.Truncated {
				continue // the cap engaged; this case is covered by the cap tests
			}

			gotDepth := map[string]int{}
			for _, n := range got.Nodes {
				gotDepth[n.Name] = n.Depth
			}
			if !reflect.DeepEqual(gotDepth, want) {
				t.Errorf("start=%q dir=%s rels=%v: traversal disagrees with the reference walk\n got %d nodes\nwant %d nodes",
					start, tc.direction, tc.relKinds, len(gotDepth), len(want))
				for name, wd := range want {
					if gd, ok := gotDepth[name]; !ok {
						t.Errorf("  missing %q (reference depth %d)", name, wd)
					} else if gd != wd {
						t.Errorf("  %q at depth %d, reference says %d", name, gd, wd)
					}
				}
				for name := range gotDepth {
					if _, ok := want[name]; !ok {
						t.Errorf("  extra %q not reachable in the reference", name)
					}
				}
				return // one failure is enough to debug
			}
		}
	}
}

// TestFindPath_MatchesReferenceReachability — a path must exist exactly when the naive
// forward walk can reach the destination, and every edge it reports must be real.
func TestFindPath_MatchesReferenceReachability(t *testing.T) {
	s := equivalenceCorpus(t)
	g := s.Graph()
	fwdAdj := materializeAdjacency(g, false)

	nodes := sampleNodes(g, 40)
	const maxDepth = 5
	for i, from := range nodes {
		reach := referenceBFS(fwdAdj, []string{from}, nil, maxDepth)
		// Probe a few destinations: some reachable, some almost certainly not.
		for _, to := range nodes[(i+1)%len(nodes) : min(i+6, len(nodes))] {
			res := g.FindPath(from, to, nil, maxDepth)
			_, reachable := reach[to]
			if res.Found != reachable {
				t.Fatalf("FindPath(%q → %q).Found = %v, reference reachability = %v",
					from, to, res.Found, reachable)
			}
			if !res.Found {
				continue
			}
			// Every hop must be an edge that exists, and the path must join up.
			for h := 1; h < len(res.Path); h++ {
				prev, cur := res.Path[h-1].Name, res.Path[h].Name
				ok := false
				for _, e := range fwdAdj[prev] {
					if e.Target == cur {
						ok = true
						break
					}
				}
				if !ok {
					t.Fatalf("FindPath(%q → %q) reports hop %q → %q, which is not an edge",
						from, to, prev, cur)
				}
			}
			if len(res.Path) > 0 && res.Path[0].Name != from {
				t.Fatalf("path starts at %q, want %q", res.Path[0].Name, from)
			}
			if len(res.Path) > 0 && res.Path[len(res.Path)-1].Name != to {
				t.Fatalf("path ends at %q, want %q", res.Path[len(res.Path)-1].Name, to)
			}
		}
	}
}

// TestDegrees_MatchMaterializedEdges — FanOut and ArchitecturalFanIn are counted
// without building the edge slices, so they can drift from the lists they summarize.
func TestDegrees_MatchMaterializedEdges(t *testing.T) {
	s := equivalenceCorpus(t)
	g := s.Graph()

	for _, name := range sampleNodes(g, 200) {
		if got, want := g.FanOut(name), len(g.ForwardEdges(name)); got != want {
			t.Errorf("FanOut(%q) = %d, but ForwardEdges returned %d", name, got, want)
		}
		if got, want := g.ArchitecturalFanIn(name), len(g.ArchitecturalReverseEdges(name)); got != want {
			t.Errorf("ArchitecturalFanIn(%q) = %d, but ArchitecturalReverseEdges returned %d", name, got, want)
		}
		if got, want := g.ArchitecturalFanOut(name), len(g.ArchitecturalForwardEdges(name)); got != want {
			t.Errorf("ArchitecturalFanOut(%q) = %d, but ArchitecturalForwardEdges returned %d", name, got, want)
		}
		// The architectural view is a strict subset of the raw index, in both directions.
		if arch, raw := g.ArchitecturalFanIn(name), len(g.ReverseEdges(name)); arch > raw {
			t.Errorf("ArchitecturalFanIn(%q) = %d exceeds raw fan-in %d", name, arch, raw)
		}
		if arch, raw := g.ArchitecturalFanOut(name), g.FanOut(name); arch > raw {
			t.Errorf("ArchitecturalFanOut(%q) = %d exceeds raw fan-out %d", name, arch, raw)
		}
	}
}

// TestMissingNames_AreHandledEverywhere — a name the graph has never seen must not
// panic any entry point, and must produce the documented empty answers. The CSR
// lookups index arrays by ID, so an unresolved name is the shape most likely to fault.
func TestMissingNames_AreHandledEverywhere(t *testing.T) {
	s := equivalenceCorpus(t)
	g := s.Graph()
	const absent = "no.such.symbol.anywhere"

	if got := g.FanOut(absent); got != 0 {
		t.Errorf("FanOut(absent) = %d, want 0", got)
	}
	if got := g.ForwardEdges(absent); got != nil {
		t.Errorf("ForwardEdges(absent) = %v, want nil", got)
	}
	if got := g.ReverseEdges(absent); got != nil {
		t.Errorf("ReverseEdges(absent) = %v, want nil", got)
	}
	if got := g.ArchitecturalFanIn(absent); got != 0 {
		t.Errorf("ArchitecturalFanIn(absent) = %d, want 0", got)
	}
	if got := g.ReverseFacts(absent, ""); got != nil {
		t.Errorf("ReverseFacts(absent) = %v, want nil", got)
	}
	if got := g.FindPath(absent, absent+"2", nil, 5); got.Found {
		t.Error("FindPath between two absent names reports a path")
	}

	// Traverse must still describe the node it was asked about, marked unresolved —
	// an empty result would not distinguish "not in the graph" from "no dependents".
	res := g.Traverse(absent, "forward", nil, nil, 3, 100)
	if len(res.Nodes) != 1 || res.Nodes[0].Name != absent || !res.Nodes[0].Unresolved {
		t.Errorf("Traverse(absent) = %+v, want a single unresolved node named %q", res.Nodes, absent)
	}

	impact := g.ImpactSet(absent, 3, 100, true)
	if impact.TotalDependents != 0 {
		t.Errorf("ImpactSet(absent).TotalDependents = %d, want 0", impact.TotalDependents)
	}
}
