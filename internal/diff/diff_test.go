package diff

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// mod builds a module fact with the given import relations.
func mod(name, file string, imports ...string) facts.Fact {
	f := facts.Fact{Kind: facts.KindModule, Name: name, File: file, Repo: "r"}
	for _, t := range imports {
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelImports, Target: t})
	}
	return f
}

func sym(name, file string, line int) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Line: line, Repo: "r",
		Props: map[string]any{"symbol_kind": facts.SymbolFunc}}
}

func cycleInsight(modules ...string) facts.Insight {
	in := facts.Insight{Source: "cycles", Title: "Dependency cycle: " + strings.Join(modules, " → "), Confidence: 1.0}
	for _, m := range modules {
		in.Evidence = append(in.Evidence, facts.Evidence{Fact: m})
	}
	return in
}

func snap(fs []facts.Fact, ins []facts.Insight) *facts.Snapshot {
	return &facts.Snapshot{Facts: fs, Insights: ins}
}

func TestCompute_AddedRemovedEdges(t *testing.T) {
	base := snap([]facts.Fact{mod("a", "a/a.go", "b"), mod("b", "b/b.go")}, nil)
	cur := snap([]facts.Fact{mod("a", "a/a.go", "b", "c"), mod("b", "b/b.go"), mod("c", "c/c.go")}, nil)

	d := Compute(base, cur)

	if len(d.FactsAdded) != 1 || d.FactsAdded[0].Name != "c" {
		t.Fatalf("expected module c added, got %+v", d.FactsAdded)
	}
	if len(d.FactsRemoved) != 0 {
		t.Fatalf("expected no removals, got %+v", d.FactsRemoved)
	}
	if len(d.EdgesAdded) != 1 || d.EdgesAdded[0].Source != "a" || d.EdgesAdded[0].Target != "c" {
		t.Fatalf("expected edge a->c added, got %+v", d.EdgesAdded)
	}
}

func TestCompute_RemovedSymbol(t *testing.T) {
	base := snap([]facts.Fact{sym("Foo", "x.go", 10), sym("Bar", "x.go", 20)}, nil)
	cur := snap([]facts.Fact{sym("Foo", "x.go", 10)}, nil)

	d := Compute(base, cur)
	if len(d.FactsRemoved) != 1 || d.FactsRemoved[0].Name != "Bar" {
		t.Fatalf("expected Bar removed, got %+v", d.FactsRemoved)
	}
}

func TestCompute_LineShiftIsNotAChange(t *testing.T) {
	// A symbol that only moved lines (props identical) must NOT register as changed —
	// otherwise any edit floods the diff with noise.
	base := snap([]facts.Fact{sym("Foo", "x.go", 10)}, nil)
	cur := snap([]facts.Fact{sym("Foo", "x.go", 42)}, nil)

	d := Compute(base, cur)
	if !d.Empty() {
		t.Fatalf("expected empty diff for line-only shift, got %+v", d)
	}
}

func TestCompute_PropsChangeIsAChange(t *testing.T) {
	a := sym("Foo", "x.go", 10)
	b := sym("Foo", "x.go", 10)
	b.Props = map[string]any{"symbol_kind": facts.SymbolFunc, "signature": "func Foo(x int)"}

	d := Compute(snap([]facts.Fact{a}, nil), snap([]facts.Fact{b}, nil))
	if len(d.FactsChanged) != 1 {
		t.Fatalf("expected 1 changed fact, got %+v", d.FactsChanged)
	}
}

func TestCompute_NewFinding(t *testing.T) {
	base := snap(nil, nil)
	cur := snap(nil, []facts.Insight{cycleInsight("a", "b")})

	d := Compute(base, cur)
	if len(d.FindingsNew) != 1 {
		t.Fatalf("expected 1 new finding, got %+v", d.FindingsNew)
	}
	if len(d.FindingsResolved) != 0 {
		t.Fatalf("expected 0 resolved, got %+v", d.FindingsResolved)
	}
}

// TestCompute_PreexistingFindingIsSilent is the false-signal guard: a finding
// present in BOTH snapshots (e.g. an API-first route unused before and after, or
// a pre-existing cycle) must never surface. The ratchet reports movement, not state.
func TestCompute_PreexistingFindingIsSilent(t *testing.T) {
	preexisting := cycleInsight("a", "b")
	base := snap(nil, []facts.Insight{preexisting})
	cur := snap(nil, []facts.Insight{preexisting})

	d := Compute(base, cur)
	if !d.Empty() {
		t.Fatalf("pre-existing finding leaked into diff: %+v", d)
	}
	if len(d.FindingsNew) != 0 {
		t.Fatalf("pre-existing finding reported as new: %+v", d.FindingsNew)
	}
}

// TestCompute_FindingStableAcrossVolatileTitle verifies a finding about the same
// entities stays identified even when its title/detail metrics change, so a
// god-class whose fan-in ticked up does not churn as resolve+new.
func TestCompute_FindingStableAcrossVolatileTitle(t *testing.T) {
	before := facts.Insight{Source: "god-class", Title: "God class Foo (fan-in 11)", Confidence: 0.6,
		Evidence: []facts.Evidence{{Symbol: "Foo", Detail: "fan-in: 11"}}}
	after := facts.Insight{Source: "god-class", Title: "God class Foo (fan-in 13)", Confidence: 0.7,
		Evidence: []facts.Evidence{{Symbol: "Foo", Detail: "fan-in: 13"}}}

	d := Compute(snap(nil, []facts.Insight{before}), snap(nil, []facts.Insight{after}))
	if !d.Empty() {
		t.Fatalf("same-entity finding churned despite identity-stable key: %+v", d)
	}
}

func TestCompute_ResolvedFinding(t *testing.T) {
	base := snap(nil, []facts.Insight{cycleInsight("a", "b")})
	cur := snap(nil, nil)

	d := Compute(base, cur)
	if len(d.FindingsResolved) != 1 {
		t.Fatalf("expected 1 resolved finding, got %+v", d.FindingsResolved)
	}
}

// TestCompute_Deterministic guards the brand promise: identical inputs render
// byte-identically regardless of input ordering or map iteration.
func TestCompute_Deterministic(t *testing.T) {
	mkBase := func() *facts.Snapshot {
		return snap([]facts.Fact{mod("a", "a.go", "b"), mod("b", "b.go")}, []facts.Insight{cycleInsight("x", "y")})
	}
	mkCur := func() *facts.Snapshot {
		return snap(
			[]facts.Fact{mod("b", "b.go", "a"), mod("a", "a.go", "b"), mod("c", "c.go")},
			[]facts.Insight{cycleInsight("a", "b"), cycleInsight("x", "y")},
		)
	}
	first := Compute(mkBase(), mkCur()).RenderSummary()
	second := Compute(mkBase(), mkCur()).RenderSummary()
	if first != second {
		t.Fatalf("non-deterministic render:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestFocused_NarrowsToArea(t *testing.T) {
	cur := snap([]facts.Fact{sym("PaymentService", "pay/svc.go", 1), sym("CartService", "cart/svc.go", 1)}, nil)
	d := Compute(snap(nil, nil), cur).Focused("payment")
	if len(d.FactsAdded) != 1 || d.FactsAdded[0].Name != "PaymentService" {
		t.Fatalf("focus did not narrow correctly: %+v", d.FactsAdded)
	}
}

// store fact helper for storage table-reference facts that share (kind,file,name).
func storageRef(name, file, operation, storageKind string, line int) facts.Fact {
	return facts.Fact{Kind: facts.KindStorage, Name: name, File: file, Line: line, Repo: "r",
		Props: map[string]any{"operation": operation, "storage_kind": storageKind, "language": "go"}}
}

// TestCompute_NoChurnFromCollidingStorageRefs is the regression guard for the
// identity-collision bug found dogfooding on a real backend: the same table is
// referenced by several SQL operations in one file, so several storage facts
// share (kind, repo, file, name). They must be keyed apart by their operation —
// otherwise the on-disk baseline (one iteration order) and in-memory current
// (another) pick different colliding representatives and the diff falsely reports
// "changed" with zero real change. Here the two snapshots hold the SAME facts in
// REVERSED order; the diff must be empty.
func TestCompute_NoChurnFromCollidingStorageRefs(t *testing.T) {
	insert := storageRef("analytics_events", "db/analytics.go", "INSERT", "table_reference", 38)
	selectF := storageRef("analytics_events", "db/analytics.go", "SELECT", "table_reference", 127)

	base := snap([]facts.Fact{insert, selectF}, nil)
	cur := snap([]facts.Fact{selectF, insert}, nil) // reversed iteration order

	d := Compute(base, cur)
	if !d.Empty() {
		t.Fatalf("colliding storage refs churned the diff: %+v", d)
	}
}

// TestCompute_NoChurnFromCollidingRouteMethods is the same guard for routes: one
// path served under multiple methods shares (kind, repo, file, name).
func TestCompute_NoChurnFromCollidingRouteMethods(t *testing.T) {
	get := facts.Fact{Kind: facts.KindRoute, Name: "/users/{id}", File: "routes.go", Line: 10, Repo: "r",
		Props: map[string]any{"method": "GET"}}
	del := facts.Fact{Kind: facts.KindRoute, Name: "/users/{id}", File: "routes.go", Line: 11, Repo: "r",
		Props: map[string]any{"method": "DELETE"}}

	d := Compute(snap([]facts.Fact{get, del}, nil), snap([]facts.Fact{del, get}, nil))
	if !d.Empty() {
		t.Fatalf("colliding route methods churned the diff: %+v", d)
	}
}

func TestRenderSummary_EmptyIsExplicit(t *testing.T) {
	d := Compute(snap(nil, nil), snap(nil, nil))
	out := d.RenderSummary()
	if !strings.Contains(out, "No architectural changes detected") {
		t.Fatalf("empty diff should state so explicitly, got:\n%s", out)
	}
}

// TestRenderSummary_NoPreexistingSection ensures the report never lists existing
// state — only regressions, improvements, and structural deltas.
func TestRenderSummary_OnlyDeltas(t *testing.T) {
	base := snap([]facts.Fact{mod("a", "a.go")}, []facts.Insight{cycleInsight("p", "q")})
	cur := snap([]facts.Fact{mod("a", "a.go"), mod("b", "b.go", "a")}, []facts.Insight{cycleInsight("p", "q"), cycleInsight("a", "b")})
	out := Compute(base, cur).RenderSummary()

	if !strings.Contains(out, "Regressions introduced (1)") {
		t.Fatalf("expected exactly 1 new regression in summary, got:\n%s", out)
	}
	// The pre-existing p→q cycle must not appear anywhere.
	if strings.Contains(out, "p → q") {
		t.Fatalf("pre-existing cycle leaked into report:\n%s", out)
	}
}
