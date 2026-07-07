package cycles

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// --- helpers ---

func makeStore(modules []string, deps map[string][]string) *facts.Store {
	s := facts.NewStore()
	for _, m := range modules {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	for src, targets := range deps {
		for _, tgt := range targets {
			s.Add(facts.Fact{
				Kind: facts.KindDependency,
				File: src + "/file.go",
				Relations: []facts.Relation{
					{Kind: facts.RelImports, Target: tgt},
				},
			})
		}
	}
	return s
}

// --- Tarjan's SCC tests ---

func TestTarjanSCC_KnownGraphs(t *testing.T) {
	tests := []struct {
		name           string
		graph          map[string][]string
		wantCycleCount int   // SCCs with size > 1
		wantCycleSizes []int // sorted sizes of non-trivial SCCs
	}{
		{
			name:           "empty graph",
			graph:          map[string][]string{},
			wantCycleCount: 0,
		},
		{
			name:           "single node no edges",
			graph:          map[string][]string{"A": nil},
			wantCycleCount: 0,
		},
		{
			name:           "simple cycle A<->B",
			graph:          map[string][]string{"A": {"B"}, "B": {"A"}},
			wantCycleCount: 1,
			wantCycleSizes: []int{2},
		},
		{
			name:           "triangle A->B->C->A",
			graph:          map[string][]string{"A": {"B"}, "B": {"C"}, "C": {"A"}},
			wantCycleCount: 1,
			wantCycleSizes: []int{3},
		},
		{
			name: "two disjoint cycles",
			graph: map[string][]string{
				"A": {"B"}, "B": {"A"},
				"C": {"D"}, "D": {"C"},
			},
			wantCycleCount: 2,
			wantCycleSizes: []int{2, 2},
		},
		{
			name:           "chain no cycle A->B->C",
			graph:          map[string][]string{"A": {"B"}, "B": {"C"}, "C": nil},
			wantCycleCount: 0,
		},
		{
			name: "complex graph: cycle with tail",
			graph: map[string][]string{
				"A": {"B"}, "B": {"C"}, "C": {"A", "D"}, "D": nil,
			},
			wantCycleCount: 1,
			wantCycleSizes: []int{3},
		},
		{
			name: "two cycles sharing a node",
			graph: map[string][]string{
				"A": {"B"}, "B": {"A", "C"}, "C": {"B"},
			},
			wantCycleCount: 1,
			wantCycleSizes: []int{3}, // A, B, C are all in one SCC
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sccs := tarjanSCC(tt.graph)
			var cycles [][]string
			for _, scc := range sccs {
				if len(scc) > 1 {
					cycles = append(cycles, scc)
				}
			}
			if len(cycles) != tt.wantCycleCount {
				t.Errorf("got %d cycles, want %d. SCCs: %v", len(cycles), tt.wantCycleCount, sccs)
				return
			}
			if tt.wantCycleSizes != nil {
				gotSizes := make([]int, len(cycles))
				for i, c := range cycles {
					gotSizes[i] = len(c)
				}
				sort.Ints(gotSizes)
				sort.Ints(tt.wantCycleSizes)
				if len(gotSizes) != len(tt.wantCycleSizes) {
					t.Errorf("cycle sizes: got %v, want %v", gotSizes, tt.wantCycleSizes)
				} else {
					for i := range gotSizes {
						if gotSizes[i] != tt.wantCycleSizes[i] {
							t.Errorf("cycle sizes[%d]: got %d, want %d", i, gotSizes[i], tt.wantCycleSizes[i])
						}
					}
				}
			}
		})
	}
}

func TestTarjanSCC_SelfLoop(t *testing.T) {
	graph := map[string][]string{"A": {"A"}}
	sccs := tarjanSCC(graph)
	// Self-loop creates an SCC of size 1 — should not panic
	for _, scc := range sccs {
		if len(scc) > 1 {
			t.Errorf("self-loop should not produce SCC > 1, got %v", scc)
		}
	}
}

// Tests for the module-graph and import-resolution helpers now live in the
// common package (common_test.go), where the shared implementations moved.

// --- Integration tests for Explain ---

func TestExplain_NoCycles(t *testing.T) {
	// Use paths with slashes so isExternalImport treats them as internal
	store := makeStore(
		[]string{"src/a", "src/b", "src/c"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/b": {"src/c"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for acyclic graph, got %d: %+v", len(insights), insights)
	}
}

func TestExplain_WithCycle(t *testing.T) {
	store := makeStore(
		[]string{"src/a", "src/b", "src/c"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/b": {"src/c"},
			"src/c": {"src/a"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 cycle insight, got %d", len(insights))
	}

	insight := insights[0]
	if insight.Confidence != 1.0 {
		t.Errorf("confidence = %f, want 1.0", insight.Confidence)
	}
	if len(insight.Evidence) != 3 {
		t.Errorf("evidence count = %d, want 3 (one per module in cycle)", len(insight.Evidence))
	}
	// Verify all three modules appear in evidence
	evidenceModules := make(map[string]bool)
	for _, ev := range insight.Evidence {
		evidenceModules[ev.Fact] = true
	}
	for _, mod := range []string{"src/a", "src/b", "src/c"} {
		if !evidenceModules[mod] {
			t.Errorf("module %q missing from cycle evidence", mod)
		}
	}
}

// TestExplain_Deterministic guards BUG-2: the cycle path, evidence order, and
// multi-cycle insight order used to depend on Go's randomized map iteration
// (tarjanSCC ranged the graph map directly and never sorted). Each Explain call
// re-ranges the maps, so 50 runs exercise many iteration orders; the fully
// rendered output (title + description + evidence facts) must be byte-identical
// every time — enola's core determinism promise for insights.json.
func TestExplain_Deterministic(t *testing.T) {
	store := makeStore(
		[]string{"src/a", "src/b", "src/c", "src/d", "src/e", "src/f"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/b": {"src/c"},
			"src/c": {"src/a"}, // cycle 1: a,b,c
			"src/d": {"src/e"},
			"src/e": {"src/f"},
			"src/f": {"src/d"}, // cycle 2: d,e,f
		},
	)

	render := func() string {
		insights, err := New().Explain(context.Background(), store)
		if err != nil {
			t.Fatalf("Explain: %v", err)
		}
		var b strings.Builder
		for _, in := range insights {
			b.WriteString(in.Title)
			b.WriteByte('\n')
			b.WriteString(in.Description)
			b.WriteByte('\n')
			for _, ev := range in.Evidence {
				b.WriteString(ev.Fact)
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		return b.String()
	}

	want := render()
	for i := 0; i < 50; i++ {
		if got := render(); got != want {
			t.Fatalf("non-deterministic output on iteration %d:\nwant:\n%s\ngot:\n%s", i, want, got)
		}
	}
}

// TestExplain_EvidenceCanonicalOrder locks that a cycle's evidence lists its
// members in sorted order (the canonicalization behind the determinism fix).
func TestExplain_EvidenceCanonicalOrder(t *testing.T) {
	store := makeStore(
		[]string{"src/c", "src/a", "src/b"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/b": {"src/c"},
			"src/c": {"src/a"},
		},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	var got []string
	for _, ev := range insights[0].Evidence {
		got = append(got, ev.Fact)
	}
	want := []string{"src/a", "src/b", "src/c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("evidence order = %v, want sorted %v", got, want)
	}
}

// TestExplain_SelfLoopNoInsight runs a self-importing module through the full
// Explain (not just tarjanSCC): a size-1 SCC is not a cycle, so no insight and
// no panic.
func TestExplain_SelfLoopNoInsight(t *testing.T) {
	store := makeStore(
		[]string{"src/a"},
		map[string][]string{"src/a": {"src/a"}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("self-loop should produce no cycle insight, got %d", len(insights))
	}
}

// TestExplain_SharedNodeSingleInsight: two cycles sharing a node collapse into
// one SCC, so Explain emits a single insight covering all three modules.
func TestExplain_SharedNodeSingleInsight(t *testing.T) {
	store := makeStore(
		[]string{"src/a", "src/b", "src/c"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/b": {"src/a", "src/c"},
			"src/c": {"src/b"},
		},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight for two cycles sharing a node, got %d", len(insights))
	}
	if len(insights[0].Evidence) != 3 {
		t.Errorf("expected all 3 modules as evidence, got %d", len(insights[0].Evidence))
	}
}

func TestExplain_EmptyStore(t *testing.T) {
	insights, err := New().Explain(context.Background(), facts.NewStore())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("empty store should yield no insights, got %d", len(insights))
	}
}

func TestExplain_MultipleCycles(t *testing.T) {
	store := makeStore(
		[]string{"src/a", "src/b", "src/c", "src/d"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/b": {"src/a"},
			"src/c": {"src/d"},
			"src/d": {"src/c"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 2 {
		t.Errorf("expected 2 cycle insights for 2 disjoint cycles, got %d", len(insights))
	}
}
