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

// TestExplain_DegreeBoundary: exactly minDegree on both sides qualifies (it's a
// clear outlier against the zero-score neighbors); one side at minDegree-1 does not.
func TestExplain_DegreeBoundary(t *testing.T) {
	at := makeStore("core.Hub", minDegree, minDegree)
	got, err := New().Explain(context.Background(), at)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("in==out==minDegree (%d) should be a hotspot, got %d insights", minDegree, len(got))
	}

	below := makeStore("core.Hub", minDegree-1, 10)
	got, err = New().Explain(context.Background(), below)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("fan-in == minDegree-1 (%d) should not qualify, got %d", minDegree-1, len(got))
	}
}

// TestExplain_NeighborsCappedAndSorted: evidence is the hub plus up to
// maxNeighbors in-callers and maxNeighbors out-callees, each sorted.
func TestExplain_NeighborsCappedAndSorted(t *testing.T) {
	store := makeStore("core.Hub", 8, 8) // more neighbors than the cap on each side
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	ev := insights[0].Evidence
	// hub + up to maxNeighbors in + up to maxNeighbors out.
	if len(ev) > 1+2*maxNeighbors {
		t.Errorf("neighbor evidence not capped: got %d, want <= %d", len(ev), 1+2*maxNeighbors)
	}
	in, out := 0, 0
	for _, e := range ev[1:] {
		switch {
		case strings.HasPrefix(e.Detail, "calls into"):
			in++
		case strings.HasPrefix(e.Detail, "called by"):
			out++
		}
	}
	if in > maxNeighbors || out > maxNeighbors {
		t.Errorf("per-side cap exceeded: in=%d out=%d, cap=%d", in, out, maxNeighbors)
	}
}

// TestExplain_OrderedByScore: the higher fanIn×fanOut hotspot ranks first.
func TestExplain_OrderedByScore(t *testing.T) {
	s := facts.NewStore()
	// Big: 6x6 = 36; Small: 3x3 = 9. Give each disjoint callers/callees.
	addHub := func(name, dir string, in, out int) {
		calls := make([]facts.Relation, 0, out)
		for i := 0; i < out; i++ {
			tgt := fmt.Sprintf("%s/t%d.Fn", dir, i)
			calls = append(calls, facts.Relation{Kind: facts.RelCalls, Target: tgt})
			s.Add(facts.Fact{Kind: facts.KindSymbol, Name: tgt, File: dir + "/t.go"})
		}
		s.Add(facts.Fact{Kind: facts.KindSymbol, Name: name, File: dir + "/h.go", Relations: calls})
		for i := 0; i < in; i++ {
			s.Add(facts.Fact{Kind: facts.KindSymbol, Name: fmt.Sprintf("%s/c%d.Fn", dir, i), File: dir + "/c.go",
				Relations: []facts.Relation{{Kind: facts.RelCalls, Target: name}}})
		}
	}
	addHub("big.Hub", "big", 6, 6)
	addHub("small.Hub", "small", 3, 3)
	s.BuildGraph()

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) < 1 || !strings.Contains(insights[0].Title, "big.Hub") {
		t.Errorf("highest-score hotspot should rank first, got %v", func() []string {
			out := make([]string, len(insights))
			for i, in := range insights {
				out[i] = in.Title
			}
			return out
		}())
	}
}
