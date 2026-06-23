package godclass

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// makeStore builds a store where each name in callers gets a symbol fact with a
// "calls" relation to hub, plus the hub symbol itself, then builds the graph.
func makeStore(hub string, callers []string, extraSymbols []string) *facts.Store {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: hub, File: "core/hub.go"})
	for _, c := range callers {
		s.Add(facts.Fact{
			Kind:      facts.KindSymbol,
			Name:      c,
			File:      "callers/" + strings.ReplaceAll(c, "/", "_") + ".go",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: hub}},
		})
	}
	for _, x := range extraSymbols {
		s.Add(facts.Fact{Kind: facts.KindSymbol, Name: x, File: "leaf/x.go"})
	}
	s.BuildGraph()
	return s
}

func manyCallers(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("pkg/c%d.Call", i)
	}
	return out
}

func TestExplain_NoGraph(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "a.B"})
	// No BuildGraph -> Graph() is nil.
	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights when graph is nil, got %d", len(insights))
	}
}

func TestExplain_DetectsGodClass(t *testing.T) {
	// One hub with 12 dependents, plus low-fan-in noise symbols.
	store := makeStore("core.Hub", manyCallers(12), []string{"leaf.A", "leaf.B", "leaf.C"})

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 god-class insight, got %d: %+v", len(insights), insights)
	}
	in := insights[0]
	if !strings.Contains(in.Title, "core.Hub") {
		t.Errorf("title %q should name the hub", in.Title)
	}
	if in.Confidence < 0.5 || in.Confidence > 1.0 {
		t.Errorf("confidence %v out of range", in.Confidence)
	}
	// First evidence is the hub itself; capped dependents follow.
	if in.Evidence[0].Symbol != "core.Hub" {
		t.Errorf("first evidence should be the hub, got %q", in.Evidence[0].Symbol)
	}
	if len(in.Evidence) > maxEvidence+1 {
		t.Errorf("evidence not capped: got %d", len(in.Evidence))
	}
}

func TestExplain_BelowFloor(t *testing.T) {
	// Hub has only 5 dependents — below minFanIn even if it's the max.
	store := makeStore("core.Hub", manyCallers(5), nil)

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights below fan-in floor, got %d", len(insights))
	}
}
