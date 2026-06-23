package explain

import (
	"strconv"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/bootstrap"
)

// newTestEngine builds a bootstrap.Engine with no config file (falls back to
// defaults) so tests can populate its store directly.
func newTestEngine(t *testing.T) *bootstrap.Engine {
	t.Helper()
	eng, _, err := bootstrap.NewEngine(bootstrap.Options{ConfigPath: "/nonexistent/mcp-arch.yaml"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng
}

// fixtureFacts builds a small but representative fact set: two modules, a few
// symbols of distinct kinds, a route, a storage table, and dependency edges that
// make module "internal/b" a coupling hotspot (fan-in 6).
func fixtureFacts() []facts.Fact {
	ff := []facts.Fact{
		{Kind: facts.KindModule, Name: "internal/a", File: "internal/a"},
		{Kind: facts.KindModule, Name: "internal/b", File: "internal/b"},
		{Kind: facts.KindSymbol, Name: "internal/a.DoThing", File: "internal/a/x.go", Line: 10,
			Props: map[string]any{"symbol_kind": facts.SymbolFunc},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: "internal/a"},
				{Kind: facts.RelCalls, Target: "internal/b.Helper"}}},
		{Kind: facts.KindSymbol, Name: "internal/b.Helper", File: "internal/b/y.go", Line: 5,
			Props:     map[string]any{"symbol_kind": facts.SymbolFunc},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: "internal/b"}}},
		{Kind: facts.KindSymbol, Name: "internal/b.Store", File: "internal/b/y.go", Line: 20,
			Props:     map[string]any{"symbol_kind": facts.SymbolStruct},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: "internal/b"}}},
		{Kind: facts.KindSymbol, Name: "internal/b.Reader", File: "internal/b/y.go", Line: 30,
			Props:     map[string]any{"symbol_kind": facts.SymbolInterface},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: "internal/b"}}},
		{Kind: facts.KindRoute, Name: "GET /things", Props: map[string]any{"method": "get"}},
		{Kind: facts.KindStorage, Name: "things", File: "internal/b/y.go"},
	}

	// Six modules each importing internal/b → fan-in 6 → medium criticality.
	for _, src := range []string{"internal/a", "internal/c", "internal/d", "internal/e", "internal/f", "internal/g"} {
		ff = append(ff, facts.Fact{
			Kind:      facts.KindDependency,
			Name:      src + " -> internal/b",
			File:      src + "/dep.go",
			Props:     map[string]any{"source": "internal"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "internal/b"}},
		})
	}
	// One external dependency.
	ff = append(ff, facts.Fact{
		Kind: facts.KindDependency, Name: "internal/a -> github.com/x/y", File: "internal/a/x.go",
		Props:     map[string]any{"source": "external"},
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: "github.com/x/y"}},
	})
	return ff
}

func computeFixture(t *testing.T) *Report {
	t.Helper()
	eng := newTestEngine(t)
	eng.Store().Add(fixtureFacts()...)
	eng.Store().BuildGraph()
	eng.SetSnapshot(&facts.Snapshot{
		Meta: facts.SnapshotMeta{
			RepoPath:    "/repo/demo",
			GeneratedAt: "2026-06-17T00:00:00Z",
			Duration:    "42ms",
			Extractors:  []string{"go"},
		},
		Insights: []facts.Insight{
			{Title: "Architecture pattern: Go-standard", Confidence: 0.85},
			{Title: "Cyclic dependency detected (3 modules)", Confidence: 1.0},
			{Title: "Layer violation: domain -> adapter", Confidence: 0.5},
			{Title: "Cross-repo dependencies (4 edges)", Confidence: 1.0},
		},
	})
	return Compute(eng)
}

func TestCompute_KindCounts(t *testing.T) {
	r := computeFixture(t)

	want := map[string]int{
		facts.KindModule:     2,
		facts.KindSymbol:     4,
		facts.KindRoute:      1,
		facts.KindStorage:    1,
		facts.KindDependency: 7,
	}
	got := map[string]int{}
	for _, kc := range r.KindCounts {
		got[kc.Label] = kc.Count
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("kind %q: got %d, want %d", k, got[k], n)
		}
	}
	if _, ok := got[facts.KindService]; ok {
		t.Errorf("service kind should be omitted when zero")
	}
}

func TestCompute_SymbolKinds(t *testing.T) {
	r := computeFixture(t)
	got := map[string]int{}
	for _, sk := range r.SymbolKinds {
		got[sk.Label] = sk.Count
	}
	if got[facts.SymbolFunc] != 2 {
		t.Errorf("function count: got %d, want 2", got[facts.SymbolFunc])
	}
	if got[facts.SymbolStruct] != 1 || got[facts.SymbolInterface] != 1 {
		t.Errorf("struct/interface counts wrong: %+v", got)
	}
	// Descending order: function (2) should come before struct/interface (1).
	if r.SymbolKinds[0].Label != facts.SymbolFunc {
		t.Errorf("symbol kinds not sorted by count desc: %+v", r.SymbolKinds)
	}
}

func TestCompute_RoutesStorageDeps(t *testing.T) {
	r := computeFixture(t)
	if r.Routes != 1 {
		t.Errorf("routes: got %d, want 1", r.Routes)
	}
	if len(r.RoutesByMethod) != 1 || r.RoutesByMethod[0].Label != "GET" {
		t.Errorf("routes by method wrong: %+v", r.RoutesByMethod)
	}
	if r.Storage != 1 {
		t.Errorf("storage: got %d, want 1", r.Storage)
	}
	src := map[string]int{}
	for _, d := range r.DepSources {
		src[d.Label] = d.Count
	}
	if src["internal"] != 6 || src["external"] != 1 {
		t.Errorf("dep sources wrong: %+v", src)
	}
}

func TestCompute_Insights(t *testing.T) {
	r := computeFixture(t)
	if r.Architecture != "Go-standard" {
		t.Errorf("architecture: got %q, want Go-standard", r.Architecture)
	}
	if r.ArchConfidence != 0.85 {
		t.Errorf("arch confidence: got %v, want 0.85", r.ArchConfidence)
	}
	if r.Cycles != 1 {
		t.Errorf("cycles: got %d, want 1", r.Cycles)
	}
	if r.LayerViolations != 1 {
		t.Errorf("layer violations: got %d, want 1", r.LayerViolations)
	}
	if r.CrossRepoEdges != 4 {
		t.Errorf("cross-repo edges: got %d, want 4", r.CrossRepoEdges)
	}
}

func TestCompute_Hotspots(t *testing.T) {
	r := computeFixture(t)
	if len(r.Hotspots) == 0 {
		t.Fatal("expected at least one hotspot")
	}
	// internal/b has fan-in 6 → medium criticality, top of the list.
	top := r.Hotspots[0]
	if top.Module != "internal/b" {
		t.Errorf("top hotspot: got %q, want internal/b", top.Module)
	}
	if top.FanIn != 6 {
		t.Errorf("internal/b fan-in: got %d, want 6", top.FanIn)
	}
	if top.Criticality != "medium" {
		t.Errorf("internal/b criticality: got %q, want medium", top.Criticality)
	}
	if r.MediumCriticality < 1 {
		t.Errorf("expected MediumCriticality >= 1, got %d", r.MediumCriticality)
	}
	// internal/b is reached (reverse) by the importing modules → blast radius > 0.
	if top.BlastRadius <= 0 {
		t.Errorf("expected positive blast radius for internal/b, got %d", top.BlastRadius)
	}
}

func TestRender_ContainsHeadlineNumbers(t *testing.T) {
	r := computeFixture(t)
	out := r.Render()
	for _, want := range []string{
		"Repository explanation: /repo/demo",
		"Architectural kinds",
		"Symbol breakdown",
		"Impact analysis (hotspots)",
		"Go-standard",
		"internal/b",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRender_ExtraSections(t *testing.T) {
	r := computeFixture(t)
	r.AddSection("Dead code (enterprise)", "  potential dead code        3\n")
	out := r.Render()
	if !strings.Contains(out, "Dead code (enterprise)") {
		t.Errorf("extra section title missing\n%s", out)
	}
	if !strings.Contains(out, "potential dead code") {
		t.Errorf("extra section body missing\n%s", out)
	}
}

// unresolvedFixtureFacts mimics a Python snapshot before import resolution:
// module names are slash paths but dependency import targets are raw dotted
// paths that match no module.
func unresolvedFixtureFacts() []facts.Fact {
	return []facts.Fact{
		{Kind: facts.KindModule, Name: "src/airflow/models", File: "src/airflow/models"},
		{Kind: facts.KindModule, Name: "src/airflow/utils", File: "src/airflow/utils"},
		{Kind: facts.KindDependency, Name: "src/airflow/utils -> airflow.models",
			File:      "src/airflow/utils/dates.py",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "airflow.models"}}},
	}
}

func TestCompute_CouplingUnresolved(t *testing.T) {
	eng := newTestEngine(t)
	eng.Store().Add(unresolvedFixtureFacts()...)
	eng.Store().BuildGraph()
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{RepoPath: "/repo/py"}})
	r := Compute(eng)

	if !r.CouplingUnresolved {
		t.Error("expected CouplingUnresolved=true when no import edge resolves")
	}
	if len(r.Hotspots) != 0 {
		t.Errorf("expected no hotspots, got %d", len(r.Hotspots))
	}
	if r.HighCriticality+r.MediumCriticality != 0 {
		t.Errorf("expected zero criticality counts, got high=%d medium=%d", r.HighCriticality, r.MediumCriticality)
	}
}

func TestCompute_CouplingResolved_NoFlag(t *testing.T) {
	// The standard fixture's dependency targets are slash module names → resolved.
	r := computeFixture(t)
	if r.CouplingUnresolved {
		t.Error("CouplingUnresolved should be false when import edges resolve")
	}
}

func TestRender_CouplingUnresolvedNote(t *testing.T) {
	eng := newTestEngine(t)
	eng.Store().Add(unresolvedFixtureFacts()...)
	eng.Store().BuildGraph()
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{RepoPath: "/repo/py"}})
	out := Compute(eng).Render()
	if !strings.Contains(out, "coupling could not be resolved") {
		t.Errorf("expected unresolved-coupling note in output\n%s", out)
	}

	// The standard (resolved) fixture must NOT carry the note.
	if std := computeFixture(t).Render(); strings.Contains(std, "coupling could not be resolved") {
		t.Errorf("resolved fixture should not show the note\n%s", std)
	}
}

// subModuleFixtureFacts mimics a Kotlin snapshot: the internal import Target is a
// type-level path one segment below the module dir (e.g. "a/b/SomeType"), and an
// external import is dotted. computeHotspots must walk up to module "a/b".
func subModuleFixtureFacts() []facts.Fact {
	return []facts.Fact{
		{Kind: facts.KindModule, Name: "a/b", File: "a/b"},
		{Kind: facts.KindModule, Name: "a/c", File: "a/c"},
		{Kind: facts.KindDependency, Name: "a/c -> a/b/SomeType", File: "a/c/User.kt",
			Props:     map[string]any{"source": "internal"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "a/b/SomeType"}}},
		{Kind: facts.KindDependency, Name: "a/c -> org.ext.Foo", File: "a/c/User.kt",
			Props:     map[string]any{"source": "external"},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "org.ext.Foo"}}},
	}
}

func TestCompute_SubModuleTargetWalkUp(t *testing.T) {
	eng := newTestEngine(t)
	eng.Store().Add(subModuleFixtureFacts()...)
	eng.Store().BuildGraph()
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{RepoPath: "/repo/kt"}})
	r := Compute(eng)

	if r.CouplingUnresolved {
		t.Error("sub-module import target should resolve via walk-up, not flag unresolved")
	}
	if len(r.Hotspots) == 0 {
		t.Fatal("expected a hotspot for module a/b")
	}
	if r.Hotspots[0].Module != "a/b" || r.Hotspots[0].FanIn != 1 {
		t.Errorf("expected a/b fan-in 1, got %+v", r.Hotspots[0])
	}
}

func TestResolveToModule(t *testing.T) {
	mods := map[string]bool{"a/b": true, "a": true}
	cases := map[string]string{
		"a/b/SomeType": "a/b", // walk up one segment
		"a/b":          "a/b", // exact module
		"a/x/y":        "a",   // walk up to ancestor module
		"org.ext.Foo":  "",    // dotted external, no '/' module
		"zzz":          "",    // unknown
	}
	for in, want := range cases {
		if got := resolveToModule(in, mods); got != want {
			t.Errorf("resolveToModule(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHelpers(t *testing.T) {
	if firstParenInt("Cross-repo dependencies (7 edges)") != 7 {
		t.Error("firstParenInt failed for 7")
	}
	if firstParenInt("no parens here") != 0 {
		t.Error("firstParenInt should be 0 with no parens")
	}
	if firstParenInt("Cyclic dependency detected (12 modules)") != 12 {
		t.Error("firstParenInt failed for 12")
	}
	if criticalityLabel(10) != "high" || criticalityLabel(5) != "medium" || criticalityLabel(1) != "low" {
		t.Error("criticalityLabel thresholds wrong")
	}
	if fileDir("internal/a/x.go") != "internal/a" {
		t.Errorf("fileDir wrong: %q", fileDir("internal/a/x.go"))
	}
	if fileDir("main.go") != "." {
		t.Errorf("fileDir of bare file should be '.', got %q", fileDir("main.go"))
	}
}

func TestCodeHealthHelpers(t *testing.T) {
	if got := nameBetween("High fan-in symbol: internal/a.DoThing (18 dependents)", "High fan-in symbol:", " ("); got != "internal/a.DoThing" {
		t.Errorf("nameBetween (paren): got %q", got)
	}
	if got := nameBetween("Large public surface: pkg/api exports 19 of 20 symbols (95%)", "Large public surface:", " exports"); got != "pkg/api" {
		t.Errorf("nameBetween (exports): got %q", got)
	}
	if got := allInts("(fan-in 64, fan-out 20)"); len(got) != 2 || got[0] != 64 || got[1] != 20 {
		t.Errorf("allInts: got %v", got)
	}
	if got := surfaceDetail(allInts("exports 19 of 20 symbols (95%)")); got != "19/20 (95%)" {
		t.Errorf("surfaceDetail: got %q", got)
	}
	if got := fanDetail([]int{64, 20}); got != "fan-in 64 / out 20" {
		t.Errorf("fanDetail: got %q", got)
	}
}

// codeHealthInsights covers all 5 new explainer titles, with 6 god-class entries
// to exercise the count vs. the top-N display cap.
func codeHealthInsights() []facts.Insight {
	ins := []facts.Insight{
		{Title: "Call-graph hotspot: pkg/x.Hub (fan-in 30, fan-out 12)"},
		{Title: "Deep dependency chain: cmd/app (depth 9)"},
		{Title: "Large public surface: pkg/api exports 40 of 44 symbols (91%)"},
		{Title: "High cyclomatic complexity: pkg/x.Parse (108)"},
	}
	// Six high-fan-in symbols, descending, to test Count=6 but Top capped at 5.
	for i, n := range []int{90, 80, 70, 60, 50, 40} {
		ins = append(ins, facts.Insight{
			Title: "High fan-in symbol: pkg/x.Sym" + string(rune('A'+i)) + " (" + strconv.Itoa(n) + " dependents)",
		})
	}
	return ins
}

func computeCodeHealth(t *testing.T) *Report {
	t.Helper()
	eng := newTestEngine(t)
	eng.Store().Add(fixtureFacts()...)
	eng.Store().BuildGraph()
	eng.SetSnapshot(&facts.Snapshot{
		Meta:     facts.SnapshotMeta{RepoPath: "/repo/health"},
		Insights: codeHealthInsights(),
	})
	return Compute(eng)
}

func TestCompute_CodeHealth(t *testing.T) {
	r := computeCodeHealth(t)

	// Fixed display order, only non-empty groups present.
	wantOrder := []string{
		"god classes (high fan-in)",
		"call-graph hotspots",
		"deep dependency chains",
		"large public surfaces",
		"complexity outliers",
	}
	if len(r.CodeHealth) != len(wantOrder) {
		t.Fatalf("got %d groups, want %d: %+v", len(r.CodeHealth), len(wantOrder), r.CodeHealth)
	}
	byLabel := map[string]FindingGroup{}
	for i, g := range r.CodeHealth {
		if g.Label != wantOrder[i] {
			t.Errorf("group %d: got %q, want %q", i, g.Label, wantOrder[i])
		}
		byLabel[g.Label] = g
	}

	god := byLabel["god classes (high fan-in)"]
	if god.Count != 6 {
		t.Errorf("god-class count: got %d, want 6", god.Count)
	}
	if len(god.Top) != topPerGroup {
		t.Errorf("god-class Top: got %d, want %d (capped)", len(god.Top), topPerGroup)
	}
	if god.Top[0].Name != "pkg/x.SymA" || god.Top[0].Detail != "90 dependents" {
		t.Errorf("god-class top item: got %+v", god.Top[0])
	}
	if d := byLabel["call-graph hotspots"].Top[0].Detail; d != "fan-in 30 / out 12" {
		t.Errorf("hotspot detail: got %q", d)
	}
	if d := byLabel["large public surfaces"].Top[0].Detail; d != "40/44 (91%)" {
		t.Errorf("surface detail: got %q", d)
	}
	if d := byLabel["deep dependency chains"].Top[0].Detail; d != "depth 9" {
		t.Errorf("depth detail: got %q", d)
	}
	if d := byLabel["complexity outliers"].Top[0].Detail; d != "complexity 108" {
		t.Errorf("complexity detail: got %q", d)
	}
}

func TestRender_CodeHealth(t *testing.T) {
	out := computeCodeHealth(t).Render()
	for _, want := range []string{
		"Code health",
		"god classes (high fan-in)",
		"pkg/x.SymA",
		"90 dependents",
		"complexity outliers",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n---\n%s", want, out)
		}
	}
}

func TestCompute_CodeHealth_OmittedWhenEmpty(t *testing.T) {
	r := computeFixture(t) // fixture has only the original 4 insight types
	if len(r.CodeHealth) != 0 {
		t.Errorf("expected no code-health groups, got %+v", r.CodeHealth)
	}
	if strings.Contains(r.Render(), "Code health") {
		t.Error("Code health section should be omitted when empty")
	}
}
