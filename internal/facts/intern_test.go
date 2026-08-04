package facts

import (
	"strings"
	"testing"
	"unsafe"
)

// sharesBacking reports whether two equal strings are the SAME allocation. Interning
// is invisible to ordinary comparison — that is the point of it — so the only way to
// assert it happened is to look at the data pointers.
func sharesBacking(a, b string) bool {
	return unsafe.StringData(a) == unsafe.StringData(b)
}

// distinctCopy returns a string equal to s but guaranteed to be a different
// allocation, standing in for what a parser produces on each file it reads.
func distinctCopy(s string) string {
	return strings.Clone(s)
}

// TestAdd_InternsFile — the repeated-field case interning exists for. Every symbol in
// a file carries that file's path, and every one arrives as its own allocation.
func TestAdd_InternsFile(t *testing.T) {
	s := NewStore()
	const path = "internal/server/server.go"
	s.Add(
		Fact{Kind: KindSymbol, Name: "A", File: distinctCopy(path)},
		Fact{Kind: KindSymbol, Name: "B", File: distinctCopy(path)},
		Fact{Kind: KindSymbol, Name: "C", File: distinctCopy(path)},
	)

	ff := s.FactsRef()
	for i := 1; i < len(ff); i++ {
		if !sharesBacking(ff[0].File, ff[i].File) {
			t.Errorf("fact %d holds its own copy of %q; File was not interned", i, path)
		}
		if ff[i].File != path {
			t.Errorf("interning changed the value: got %q want %q", ff[i].File, path)
		}
	}
}

// TestAdd_InternsRelationTargets — the larger of the two populations: 5.42M relation
// targets over 1.27M distinct values on the Linux kernel.
func TestAdd_InternsRelationTargets(t *testing.T) {
	s := NewStore()
	const target = "internal/tokens.Decode"
	s.Add(
		Fact{Kind: KindSymbol, Name: "A", Relations: []Relation{{Kind: RelCalls, Target: distinctCopy(target)}}},
		Fact{Kind: KindSymbol, Name: "B", Relations: []Relation{{Kind: RelCalls, Target: distinctCopy(target)}}},
	)

	ff := s.FactsRef()
	if !sharesBacking(ff[0].Relations[0].Target, ff[1].Relations[0].Target) {
		t.Error("equal relation targets hold separate allocations; Target was not interned")
	}
	if ff[1].Relations[0].Target != target {
		t.Errorf("interning changed the value: got %q want %q", ff[1].Relations[0].Target, target)
	}
}

// TestAdd_InterningSurvivesGraphBuild — BuildGraph drops the interning table, which is
// only safe if adding more facts afterwards still works. Interning is an optimization,
// so a rebuilt table may be less effective, but nothing may break.
func TestAdd_InterningSurvivesGraphBuild(t *testing.T) {
	s := NewStore()
	s.Add(Fact{Kind: KindSymbol, Name: "A", File: distinctCopy("a.go")})
	s.BuildGraph()

	if s.intern != nil {
		t.Error("BuildGraph left the interning table allocated")
	}

	s.Add(
		Fact{Kind: KindSymbol, Name: "B", File: distinctCopy("b.go")},
		Fact{Kind: KindSymbol, Name: "C", File: distinctCopy("b.go")},
	)
	if s.Count() != 3 {
		t.Fatalf("facts added after BuildGraph were lost: %d", s.Count())
	}
	ff := s.FactsRef()
	if !sharesBacking(ff[1].File, ff[2].File) {
		t.Error("interning did not resume after BuildGraph released the table")
	}
	if got := s.ByFile("b.go"); len(got) != 2 {
		t.Errorf("byFile index disagrees after re-interning: %d facts, want 2", len(got))
	}
}

// TestAdd_EmptyStringsAreNotInterned — "" is the common case for File on module facts;
// putting it in the table would be a wasted entry and a wasted lookup.
func TestAdd_EmptyStringsAreNotInterned(t *testing.T) {
	s := NewStore()
	s.Add(Fact{Kind: KindModule, Name: "m"})
	if _, ok := s.intern[""]; ok {
		t.Error("the empty string was interned")
	}
}

// TestAll_CopiesRelations is the aliasing guarantee the append-mode path in
// engine.GenerateSnapshot depends on: it copies the PUBLISHED bundle's facts so it can
// mutate them freely while concurrent MCP readers traverse the original. A plain slice
// copy left every Relations header pointing into the published store, so rewriting a
// relation — or interning its target — wrote through into a graph being read.
func TestAll_CopiesRelations(t *testing.T) {
	s := NewStore()
	s.Add(Fact{Kind: KindSymbol, Name: "A", Relations: []Relation{
		{Kind: RelCalls, Target: "B"},
	}})

	got := s.All()
	got[0].Relations[0].Target = "MUTATED"

	if orig := s.FactsRef()[0].Relations[0].Target; orig != "B" {
		t.Errorf("mutating the copy wrote through to the store: target is now %q", orig)
	}
}

// TestAll_CopiesRelationsUnderAppend — the same guarantee against append rather than
// assignment, which is how the binders (grpcimpl, httphandler, emberresolver) add
// edges. An append into spare capacity is the quieter half of the aliasing bug.
func TestAll_CopiesRelationsUnderAppend(t *testing.T) {
	s := NewStore()
	rels := make([]Relation, 1, 4) // spare capacity: append writes in place
	rels[0] = Relation{Kind: RelCalls, Target: "B"}
	s.Add(Fact{Kind: KindSymbol, Name: "A", Relations: rels})

	got := s.All()
	got[0].Relations = append(got[0].Relations, Relation{Kind: RelCalls, Target: "C"})

	if n := len(s.FactsRef()[0].Relations); n != 1 {
		t.Errorf("appending to the copy grew the store's fact to %d relations", n)
	}
}
