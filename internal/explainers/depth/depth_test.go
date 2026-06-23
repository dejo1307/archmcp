package depth

import (
	"context"
	"fmt"
	"strings"
	"testing"

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
