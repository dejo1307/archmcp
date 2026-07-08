package depth

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/facts"
)

// makeStore builds module facts and dependency facts encoding module -> module
// imports. Module names must contain a slash and no dot so they are treated as
// internal by the shared graph builder.
func makeStore(modules []string, deps map[string][]string) *facts.Store {
	s := facts.NewStore()
	for _, m := range modules {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	for src, targets := range deps {
		for _, tgt := range targets {
			s.Add(facts.Fact{
				Kind:      facts.KindDependency,
				File:      src + "/file.go",
				Relations: []facts.Relation{{Kind: facts.RelImports, Target: tgt}},
			})
		}
	}
	return s
}

// chain builds modules a/m0..a/m{n-1} linked m0->m1->...->m{n-1}.
func chain(n int) ([]string, map[string][]string) {
	mods := make([]string, n)
	deps := map[string][]string{}
	for i := 0; i < n; i++ {
		mods[i] = fmt.Sprintf("a/m%d", i)
	}
	for i := 0; i < n-1; i++ {
		deps[mods[i]] = []string{mods[i+1]}
	}
	return mods, deps
}

func TestExplain_ShallowGraph(t *testing.T) {
	mods, deps := chain(4) // longest chain = 4 < minDepth
	insights, err := New().Explain(context.Background(), makeStore(mods, deps))
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for shallow graph, got %d", len(insights))
	}
}

func TestExplain_DeepChain(t *testing.T) {
	mods, deps := chain(5) // a/m0 has a chain of length 5
	insights, err := New().Explain(context.Background(), makeStore(mods, deps))
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 deep-chain insight, got %d: %+v", len(insights), insights)
	}
	in := insights[0]
	if !strings.Contains(in.Title, "a/m0") {
		t.Errorf("deepest module a/m0 should be reported, got title %q", in.Title)
	}
	if len(in.Evidence) != 5 {
		t.Errorf("evidence should list the 5-module chain, got %d", len(in.Evidence))
	}
}

func TestExplain_CycleTerminates(t *testing.T) {
	// a -> b -> c -> a is a cycle; must not infinite-loop, and depth stays small.
	store := makeStore(
		[]string{"a/x", "a/y", "a/z"},
		map[string][]string{
			"a/x": {"a/y"},
			"a/y": {"a/z"},
			"a/z": {"a/x"},
		},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	// Longest cycle-safe chain is 3 (< minDepth), so no insights, and no hang.
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for short cycle, got %d", len(insights))
	}
}

// cyclicGraph: a 3-module cycle x->y->z->x with a tail z->t1->t2->t3. The
// longest simple chain from x visits 6 distinct modules. A correct, cycle-safe
// longest-path must not double-count the cycle entry.
func cyclicGraph() ([]string, map[string][]string) {
	return []string{"a/x", "a/y", "a/z", "a/t1", "a/t2", "a/t3"},
		map[string][]string{
			"a/x":  {"a/y"},
			"a/y":  {"a/z"},
			"a/z":  {"a/x", "a/t1"},
			"a/t1": {"a/t2"},
			"a/t2": {"a/t3"},
		}
}

func TestExplain_CycleDoesNotInflateDepth(t *testing.T) {
	mods, deps := cyclicGraph()
	insights, err := New().Explain(context.Background(), makeStore(mods, deps))
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) == 0 {
		t.Fatal("expected at least one deep-chain insight")
	}
	deepest := insights[0]
	// The deepest chain is x->y->z->t1->t2->t3: 6 distinct modules, none repeated.
	if got := len(deepest.Evidence); got != 6 {
		t.Errorf("deepest chain should span 6 distinct modules, got %d: %q", got, deepest.Title)
	}
	seen := map[string]bool{}
	for _, ev := range deepest.Evidence {
		if seen[ev.Fact] {
			t.Errorf("chain double-counts module %q (cycle inflation): %q", ev.Fact, deepest.Title)
		}
		seen[ev.Fact] = true
	}
}

// TestExplain_CycleUnderCountRegression guards BUG-3: the old memoized
// longestChain cached a node's depth even when it was truncated by the cycle
// back-edge cut, then reused that truncated value globally. In this graph
// (m/a<->m/b cycle, tail m/c->m/d->m/e, feeder m/r->m/b) computing m/a first
// memoized m/b at depth 1, so m/r's true depth-6 chain (r->b->a->c->d->e) was
// never reported. The SCC-condensation rewrite reports it correctly.
func TestExplain_CycleUnderCountRegression(t *testing.T) {
	store := makeStore(
		[]string{"m/a", "m/b", "m/c", "m/d", "m/e", "m/r"},
		map[string][]string{
			"m/a": {"m/b", "m/c"},
			"m/b": {"m/a"},
			"m/c": {"m/d"},
			"m/d": {"m/e"},
			"m/r": {"m/b"},
		},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	byModule := map[string]facts.Insight{}
	for _, in := range insights {
		for _, m := range []string{"m/r", "m/a"} {
			if strings.Contains(in.Title, m+" (") {
				byModule[m] = in
			}
		}
	}

	rIn, ok := byModule["m/r"]
	if !ok {
		t.Fatalf("m/r (true depth 6) was not reported — under-count regression; got %+v", titles(insights))
	}
	if !strings.Contains(rIn.Title, "depth 6") {
		t.Errorf("m/r should be reported at depth 6, got title %q", rIn.Title)
	}
	if len(rIn.Evidence) != 6 {
		t.Errorf("m/r chain should span 6 distinct modules, got %d", len(rIn.Evidence))
	}
	seen := map[string]bool{}
	for _, ev := range rIn.Evidence {
		if seen[ev.Fact] {
			t.Errorf("chain double-counts %q", ev.Fact)
		}
		seen[ev.Fact] = true
	}
	// The cycle members m/a and m/b share a depth of 5 (>= minDepth) and must be
	// reported too — as a single component insight keyed on the smallest member.
	if _, ok := byModule["m/a"]; !ok {
		t.Errorf("cycle component (m/a) at depth 5 should be reported; got %v", titles(insights))
	}
}

func titles(ins []facts.Insight) []string {
	out := make([]string, len(ins))
	for i, in := range ins {
		out[i] = in.Title
	}
	return out
}

func TestExplain_CapsAtMaxInsights(t *testing.T) {
	// A single long chain: a/m0..a/m15. Modules a/m0..a/m11 have depth >= minDepth
	// (5), i.e. 12 qualifying modules — more than the cap of 10.
	mods, deps := chain(16)
	insights, err := New().Explain(context.Background(), makeStore(mods, deps))
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != maxInsights {
		t.Fatalf("expected output capped at %d, got %d", maxInsights, len(insights))
	}
	if !strings.Contains(insights[0].Title, "a/m0 (") || !strings.Contains(insights[0].Title, "depth 16") {
		t.Errorf("deepest module a/m0 (depth 16) should rank first, got %q", insights[0].Title)
	}
}

func TestExplain_SelfImportDoesNotAddDepth(t *testing.T) {
	// a/m0 imports itself and a/m1; the self-import must not inflate its depth.
	mods, deps := chain(5)
	deps["a/m0"] = append(deps["a/m0"], "a/m0")
	insights, err := New().Explain(context.Background(), makeStore(mods, deps))
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if !strings.Contains(insights[0].Title, "depth 5") {
		t.Errorf("self-import should not add depth; want depth 5, got %q", insights[0].Title)
	}
}

// TestExplain_OversizedClusterNotDeep: a large autoload cluster (a ring of
// OversizedClusterModules+3 modules) must count as ONE logical layer, not its full
// size, so it does not masquerade as a deep dependency chain. With a short tail
// below it the whole graph's real layering stays under minDepth and nothing is
// reported — matching the cycles explainer already covering the cluster.
func TestExplain_OversizedClusterNotDeep(t *testing.T) {
	n := common.OversizedClusterModules + 3
	mods := make([]string, 0, n+1)
	deps := map[string][]string{}
	ring := make([]string, n)
	for i := 0; i < n; i++ {
		ring[i] = fmt.Sprintf("c/m%02d", i)
		mods = append(mods, ring[i])
	}
	for i := 0; i < n; i++ {
		deps[ring[i]] = []string{ring[(i+1)%n]} // one big SCC
	}
	// A short 2-module tail hanging off the cluster: c/m00 -> t/t0 -> t/t1.
	mods = append(mods, "t/t0", "t/t1")
	deps["c/m00"] = append(deps["c/m00"], "t/t0")
	deps["t/t0"] = []string{"t/t1"}

	insights, err := New().Explain(context.Background(), makeStore(mods, deps))
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	// Cluster weighted as 1 + tail(2) = depth 3 < minDepth(5) -> no findings, and
	// crucially not a "depth ~N" report of the whole cluster.
	if len(insights) != 0 {
		t.Fatalf("oversized cluster should not produce a deep-chain finding, got %d: %v", len(insights), titles(insights))
	}
}

func TestExplain_EmptyGraph(t *testing.T) {
	insights, err := New().Explain(context.Background(), facts.NewStore())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("empty store should yield no insights, got %d", len(insights))
	}
}

// TestExplain_Deterministic guards against the regression where cycle handling
// depended on Go's randomized map iteration order. Each Explain call re-ranges
// the graph map, so repeated calls exercise different iteration orders; the
// rendered titles must be identical every time.
func TestExplain_Deterministic(t *testing.T) {
	mods, deps := cyclicGraph()
	store := makeStore(mods, deps)

	titles := func() []string {
		insights, err := New().Explain(context.Background(), store)
		if err != nil {
			t.Fatalf("Explain: %v", err)
		}
		out := make([]string, len(insights))
		for i, in := range insights {
			out[i] = in.Title
		}
		return out
	}

	want := strings.Join(titles(), "\n")
	for i := 0; i < 50; i++ {
		if got := strings.Join(titles(), "\n"); got != want {
			t.Fatalf("non-deterministic output on iteration %d:\nwant:\n%s\ngot:\n%s", i, want, got)
		}
	}
}
