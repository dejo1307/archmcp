package facts

// Memory ratchets. The store and graph were reshaped to hold a large repository's
// facts in a bounded footprint — interned strings, and a CSR graph index instead of
// two map[string][]Edge plus a map[string][]int. Those wins are invisible to every
// behavioural test in this package: a graph that allocates a slice per node answers
// exactly the same questions as one that does not.
//
// So they are pinned here, as budgets per fact rather than absolute numbers, measured
// on a synthetic graph big enough for the per-fact costs to dominate the fixed ones.
// The ceilings are deliberately loose — several times the measured value — because the
// failure they exist to catch is a return to per-node allocation, which costs orders
// of magnitude, not percent.

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// memCorpus is the synthetic graph size. Large enough that fixed overheads (the
// interning tables, the runtime's own allocations) are noise against the per-fact
// cost, small enough to keep the test quick.
const memCorpus = 200_000

// buildMemCorpus creates memCorpus symbol facts in a shape that mirrors real
// extraction: many symbols per file, each calling a handful of others, with the same
// callees named repeatedly — which is what makes interning and CSR worth having.
func buildMemCorpus(n int) *Store {
	s := NewStore()
	batch := make([]Fact, 0, 1024)
	for i := 0; i < n; i++ {
		f := Fact{
			Kind: KindSymbol,
			Name: fmt.Sprintf("pkg%03d.Type%04d.Method%d", i%500, i%5000, i),
			// One distinct path per 200 symbols, each occurrence its own allocation,
			// exactly as a parser emits them.
			File: fmt.Sprintf("internal/pkg%03d/file%03d.go", i%500, (i/200)%500),
			Line: i % 900,
			Relations: []Relation{
				{Kind: RelCalls, Target: fmt.Sprintf("pkg%03d.Type%04d.Method%d", (i+1)%500, (i+7)%5000, (i*3)%n)},
				{Kind: RelCalls, Target: fmt.Sprintf("pkg%03d.Helper", (i+2)%500)},
				{Kind: RelImplements, Target: fmt.Sprintf("pkg%03d.Iface", i%500)},
			},
		}
		batch = append(batch, f)
		if len(batch) == cap(batch) {
			s.Add(batch...)
			batch = batch[:0]
		}
	}
	s.Add(batch...)
	return s
}

// heapNow returns the live heap after a settling GC. Two collections because the
// first can leave finalizer-reachable objects that the second reclaims.
func heapNow() (bytes uint64, objects uint64) {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc, ms.HeapObjects
}

// TestGraphIndex_MemoryBudget is the ratchet on the CSR graph index.
//
// Object count is the sharper of the two signals. The map-of-slices graph allocated a
// slice per node in each direction plus a map bucket per key — about 2 live objects
// per fact, 4.0M of them on the Linux kernel. CSR allocates a fixed handful of arrays
// however large the graph is, so any reintroduction of per-node allocation shows up
// here as several orders of magnitude, not a few percent.
func TestGraphIndex_MemoryBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a 200k-fact graph")
	}
	s := buildMemCorpus(memCorpus)

	beforeBytes, beforeObjects := heapNow()
	s.BuildGraph()
	afterBytes, afterObjects := heapNow()

	if s.Graph() == nil {
		t.Fatal("BuildGraph produced no graph")
	}

	indexBytes := int64(afterBytes) - int64(beforeBytes)
	indexObjects := int64(afterObjects) - int64(beforeObjects)
	bytesPerFact := float64(indexBytes) / float64(memCorpus)
	objectsPerFact := float64(indexObjects) / float64(memCorpus)
	t.Logf("graph index: %d bytes (%.1f/fact), %d objects (%.4f/fact) over %d facts, %d edges",
		indexBytes, bytesPerFact, indexObjects, objectsPerFact, memCorpus, s.Graph().EdgeCount())

	// CSR measures ~90 bytes/fact here (three edges per fact, forward and reverse).
	// The map-based index measured ~450.
	const maxBytesPerFact = 250
	if bytesPerFact > maxBytesPerFact {
		t.Errorf("graph index costs %.1f bytes/fact, budget is %d — the index is allocating per node again",
			bytesPerFact, maxBytesPerFact)
	}

	// CSR allocates a constant number of arrays, so this is ~0.0001. Anything
	// approaching 1 means per-node slices are back.
	const maxObjectsPerFact = 0.05
	if objectsPerFact > maxObjectsPerFact {
		t.Errorf("graph index holds %.4f live objects/fact, budget is %.2f — per-node allocation has returned",
			objectsPerFact, maxObjectsPerFact)
	}
}

// TestStore_InterningMemoryBudget is the ratchet on Add's interning. Without it, each
// fact holds its own copy of a file path and its own copy of every relation target,
// none of which the parser could know were repeats.
func TestStore_InterningMemoryBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a 200k-fact store")
	}
	beforeBytes, _ := heapNow()
	s := buildMemCorpus(memCorpus)
	// Drop the interning table so this measures what the store RETAINS, which is what
	// a long-running server holds, not the transient cost of building it.
	s.BuildGraph()
	afterBytes, _ := heapNow()

	storeBytes := int64(afterBytes) - int64(beforeBytes)
	bytesPerFact := float64(storeBytes) / float64(memCorpus)
	t.Logf("store+graph: %d bytes (%.1f/fact) over %d facts", storeBytes, bytesPerFact, memCorpus)

	// Measured ~570 bytes/fact for this corpus (no Props, three relations each).
	// Interning the file paths and the repeated call targets is worth ~90 of that.
	const maxBytesPerFact = 1100
	if bytesPerFact > maxBytesPerFact {
		t.Errorf("store costs %.1f bytes/fact, budget is %d — strings are no longer being shared",
			bytesPerFact, maxBytesPerFact)
	}

	if s.intern != nil {
		t.Error("BuildGraph should have released the interning table")
	}
}

// TestGraphConsistency_LargeCorpus runs the CSR invariants over a real fact set —
// pointed at a facts.jsonl from a snapshot of an actual repository. The synthetic
// corpus above is regular by construction; real extraction produces name collisions,
// dangling targets, self-edges and duplicate relations, which is where an index built
// by counting sort can go wrong in ways a tidy fixture never exercises.
func TestGraphConsistency_LargeCorpus(t *testing.T) {
	path := os.Getenv("ENOLA_FACTS_CORPUS")
	if path == "" {
		t.Skip("set ENOLA_FACTS_CORPUS=<facts.jsonl> to run against a real snapshot")
	}
	s := NewStore()
	if err := s.ReadJSONLFile(path); err != nil {
		t.Fatal(err)
	}
	s.BuildGraph()
	g := s.Graph()
	t.Logf("corpus: %d facts, %d nodes, %d edges", s.Count(), g.NodeCount(), g.EdgeCount())

	// Every forward edge must appear exactly once in the reverse index, and vice
	// versa. A counting sort that mis-fills either direction breaks this.
	if len(g.fwdTgt) != len(g.revTgt) {
		t.Fatalf("forward holds %d edges, reverse holds %d", len(g.fwdTgt), len(g.revTgt))
	}
	fwdPairs := make(map[[3]uint32]int, len(g.fwdTgt))
	for src := uint32(0); src < uint32(len(g.fwdOff)-1); src++ {
		for i := g.fwdOff[src]; i < g.fwdOff[src+1]; i++ {
			fwdPairs[[3]uint32{src, g.fwdTgt[i], uint32(g.fwdRel[i])}]++
		}
	}
	for tgt := uint32(0); tgt < uint32(len(g.revOff)-1); tgt++ {
		for i := g.revOff[tgt]; i < g.revOff[tgt+1]; i++ {
			k := [3]uint32{g.revTgt[i], tgt, uint32(g.revRel[i])}
			n, ok := fwdPairs[k]
			if !ok {
				t.Fatalf("reverse edge %s <- %s has no forward counterpart",
					g.names[tgt], g.names[g.revTgt[i]])
			}
			if n != 1 {
				t.Fatalf("edge %s -> %s appears %d times in the forward index; dedup failed",
					g.names[g.revTgt[i]], g.names[tgt], n)
			}
			delete(fwdPairs, k)
		}
	}
	if len(fwdPairs) != 0 {
		t.Fatalf("%d forward edges have no reverse counterpart", len(fwdPairs))
	}

	// Offsets must be monotonic and cover the arrays exactly — the invariant that
	// makes a CSR range lookup safe.
	for i := 1; i < len(g.fwdOff); i++ {
		if g.fwdOff[i] < g.fwdOff[i-1] {
			t.Fatalf("forward offsets are not monotonic at node %d", i)
		}
	}
	if int(g.fwdOff[len(g.fwdOff)-1]) != len(g.fwdTgt) {
		t.Fatalf("forward offsets end at %d but the target array holds %d",
			g.fwdOff[len(g.fwdOff)-1], len(g.fwdTgt))
	}

	// Every declared node must resolve to a fact, and every node ID must name itself.
	for id := uint32(0); id < g.declaredNodes; id++ {
		if _, ok := g.factIndexForID(id, ""); !ok {
			t.Fatalf("declared node %d (%q) resolves to no fact", id, g.names[id])
		}
		if got, ok := g.ids[g.names[id]]; !ok || got != id {
			t.Fatalf("node %d names %q, which maps back to %d (ok=%v)", id, g.names[id], got, ok)
		}
	}
}
