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

// TestExplain_DedupReopenedSymbol: a constant reopened across many files yields
// one symbol fact per file, all sharing a Name and therefore identical in/out
// degree. Report the name once instead of once per reopening.
func TestExplain_DedupReopenedSymbol(t *testing.T) {
	store := makeStore("core.Hub", 5, 5)
	// Simulate the same constant reopened in 3 more files (no extra edges).
	for i := 0; i < 3; i++ {
		store.Add(facts.Fact{Kind: facts.KindSymbol, Name: "core.Hub", File: fmt.Sprintf("reopen/%d.go", i)})
	}
	store.BuildGraph()

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("reopened symbol should yield 1 hotspot insight, got %d: %+v", len(insights), insights)
	}
}

// TestExplain_RubyBaseClassExcluded: a Rails base class (.rb) with high fan-in AND
// fan-out is still excluded from hotspots (its inbound degree is inheritance), while
// a same-degree domain type is reported.
func TestExplain_RubyBaseClassExcluded(t *testing.T) {
	s := facts.NewStore()
	addHub := func(name, file string) {
		calls := make([]facts.Relation, 0, 4)
		for i := 0; i < 4; i++ {
			tgt := fmt.Sprintf("%s_dep%d.Fn", name, i)
			calls = append(calls, facts.Relation{Kind: facts.RelCalls, Target: tgt})
			s.Add(facts.Fact{Kind: facts.KindSymbol, Name: tgt, File: "dep.rb"})
		}
		s.Add(facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Relations: calls})
		for i := 0; i < 4; i++ {
			s.Add(facts.Fact{Kind: facts.KindSymbol, Name: fmt.Sprintf("%s_caller%d.Fn", name, i),
				File: "caller.rb", Relations: []facts.Relation{{Kind: facts.RelCalls, Target: name}}})
		}
	}
	addHub("NotifierBase", "app/notifiers/notifier_base.rb")
	addHub("User", "app/models/user.rb")
	s.BuildGraph()

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	var sawBase, sawUser bool
	for _, in := range insights {
		if strings.Contains(in.Title, "NotifierBase") {
			sawBase = true
		}
		if strings.Contains(in.Title, "User") {
			sawUser = true
		}
	}
	if sawBase {
		t.Errorf("framework base class should be excluded from hotspots")
	}
	if !sawUser {
		t.Errorf("real domain hotspot User should still be reported")
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

// TestExplain_ExcludesTestRefFanIn: a pinch point's fan-in counts only
// architectural (symbol) callers. test_ref/file_ref facts carry RelCalls edges
// into the hub but are not symbols; counting them inflates the centrality score
// and the outlier distribution (GAP-XL-15).
func TestExplain_ExcludesTestRefFanIn(t *testing.T) {
	s := facts.NewStore()
	const hub = "core.Hub"
	// Fan-out: hub calls 5 targets.
	calls := make([]facts.Relation, 0, 5)
	for i := 0; i < 5; i++ {
		tgt := fmt.Sprintf("dep/t%d.Fn", i)
		calls = append(calls, facts.Relation{Kind: facts.RelCalls, Target: tgt})
		s.Add(facts.Fact{Kind: facts.KindSymbol, Name: tgt, File: "dep/t.go"})
	}
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: hub, File: "core/hub.go", Relations: calls})
	// 4 real symbol callers (fan-in) — clears minDegree on symbols alone.
	for i := 0; i < 4; i++ {
		s.Add(facts.Fact{
			Kind: facts.KindSymbol, Name: fmt.Sprintf("caller/c%d.Fn", i),
			File:      "caller/c.go",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: hub}},
		})
	}
	// 3 reference-only callers that must not count toward fan-in.
	for i := 0; i < 2; i++ {
		s.Add(facts.Fact{
			Kind: facts.KindTestRef, Name: fmt.Sprintf("spec/c%d_spec.rb", i),
			File:      fmt.Sprintf("spec/c%d_spec.rb", i),
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: hub}},
		})
	}
	s.Add(facts.Fact{
		Kind: facts.KindFileRef, Name: "config/init.rb", File: "config/init.rb",
		Relations: []facts.Relation{{Kind: facts.RelCalls, Target: hub}},
	})
	s.BuildGraph()

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	var hubInsight *facts.Insight
	for i := range insights {
		if strings.Contains(insights[i].Title, hub) {
			hubInsight = &insights[i]
			break
		}
	}
	if hubInsight == nil {
		t.Fatalf("hub %q not reported as a hotspot; got %v", hub, insights)
	}
	// Fan-in must be 4 (symbols), not 7 (symbols + 2 test_ref + 1 file_ref).
	if !strings.Contains(hubInsight.Title, "fan-in 4") {
		t.Errorf("fan-in should exclude reference-only facts; title = %q, want fan-in 4", hubInsight.Title)
	}
	if strings.Contains(hubInsight.Title, "fan-in 7") {
		t.Errorf("fan-in wrongly includes test_ref/file_ref facts; title = %q", hubInsight.Title)
	}
	// No spec file or initializer should appear as an in-caller in the evidence.
	for _, ev := range hubInsight.Evidence[1:] {
		if strings.HasPrefix(ev.Symbol, "spec/") || strings.HasPrefix(ev.Symbol, "config/") {
			t.Errorf("reference-only fact %q leaked into hotspot evidence", ev.Symbol)
		}
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

// TestExplain_TestSupportSymbolExcluded is the hotspots half of the same gate the
// god-class explainer carries. See godclass_test.go for the full rationale: a
// symbol under a test tree is an ordinary symbol fact (not a test_ref), so
// ArchitecturalReverse cannot see it, and an XCTest helper with 1371 callers was
// being ranked as the repo's most central symbol.
//
// The production hotspot must keep its fan-in intact — the gate is on the
// candidate, not on the edges.
func TestExplain_TestSupportSymbolExcluded(t *testing.T) {
	s := facts.NewStore()
	// Test helper: high fan-in AND high fan-out, so it clears both degree floors.
	helperCalls := make([]facts.Relation, 0, 4)
	for i := 0; i < 4; i++ {
		tgt := fmt.Sprintf("Tests/Testability.Helper%d", i)
		helperCalls = append(helperCalls, facts.Relation{Kind: facts.RelCalls, Target: tgt})
		s.Add(facts.Fact{Kind: facts.KindSymbol, Name: tgt, File: "Tests/Testability/Sources/H.swift"})
	}
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "Tests/Testability/Sources.Assert",
		File: "Tests/Testability/Sources/Assert.swift", Relations: helperCalls})

	// Production hotspot: high fan-in and fan-out.
	prodCalls := make([]facts.Relation, 0, 4)
	for i := 0; i < 4; i++ {
		tgt := fmt.Sprintf("Sources/Core.Dep%d", i)
		prodCalls = append(prodCalls, facts.Relation{Kind: facts.RelCalls, Target: tgt})
		s.Add(facts.Fact{Kind: facts.KindSymbol, Name: tgt, File: "Sources/Core/Dep.swift"})
	}
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "Sources/Core.APIService",
		File: "Sources/Core/APIService.swift", Relations: prodCalls})

	for i := 0; i < 12; i++ {
		s.Add(facts.Fact{
			Kind: facts.KindSymbol,
			Name: fmt.Sprintf("Tests/CoreTests.SpecCase%d", i),
			File: fmt.Sprintf("Tests/CoreTests/SpecCase%d.swift", i),
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "Tests/Testability/Sources.Assert"},
				{Kind: facts.RelCalls, Target: "Sources/Core.APIService"},
			},
		})
	}
	s.BuildGraph()

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	var sawAPI bool
	for _, in := range insights {
		if strings.Contains(in.Title, "Assert") {
			t.Errorf("test-support symbol reported as hotspot: %q", in.Title)
		}
		if strings.Contains(in.Title, "APIService") {
			sawAPI = true
			if !strings.Contains(in.Title, "fan-in 12") {
				t.Errorf("production hotspot fan-in was altered by the test gate: %q", in.Title)
			}
		}
	}
	if !sawAPI {
		t.Errorf("production hotspot should still be reported")
	}
}
