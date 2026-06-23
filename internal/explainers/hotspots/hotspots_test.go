package hotspots

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// makeStore builds a hub symbol with the given fan-out (calls to fresh targets)
// and fan-in (callers calling the hub), plus the target/caller symbols.
func makeStore(hub string, fanIn, fanOut int) *facts.Store {
	s := facts.NewStore()

	calls := make([]facts.Relation, 0, fanOut)
	for i := 0; i < fanOut; i++ {
		tgt := fmt.Sprintf("dep/t%d.Fn", i)
		calls = append(calls, facts.Relation{Kind: facts.RelCalls, Target: tgt})
		s.Add(facts.Fact{Kind: facts.KindSymbol, Name: tgt, File: "dep/t.go"})
	}
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: hub, File: "core/hub.go", Relations: calls})

	for i := 0; i < fanIn; i++ {
		caller := fmt.Sprintf("caller/c%d.Fn", i)
		s.Add(facts.Fact{
			Kind:      facts.KindSymbol,
			Name:      caller,
			File:      "caller/c.go",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: hub}},
		})
	}

	s.BuildGraph()
	return s
}

func TestExplain_NoGraph(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "a.B"})
	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights when graph is nil, got %d", len(insights))
	}
}

func TestExplain_DetectsHotspot(t *testing.T) {
	store := makeStore("core.Hub", 5, 5) // score 25, clear outlier vs the 0-score neighbors

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 hotspot insight, got %d: %+v", len(insights), insights)
	}
	in := insights[0]
	if !strings.Contains(in.Title, "core.Hub") {
		t.Errorf("title %q should name the hub", in.Title)
	}
	if in.Evidence[0].Symbol != "core.Hub" {
		t.Errorf("first evidence should be the hub, got %q", in.Evidence[0].Symbol)
	}
}

func TestExplain_BelowDegreeFloor(t *testing.T) {
	// High fan-out but fan-in below minDegree -> not a pinch point.
	store := makeStore("core.Hub", 1, 10)

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights when one side is below the degree floor, got %d", len(insights))
	}
}
