package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/facts"
)

func TestBuildGraphViewPreservesTypedDuplicateNamesAndUnresolvedTargets(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{
			Kind: facts.KindModule, Name: "pkg", Repo: "repo",
			File: "repo/pkg/types.go", Relations: []facts.Relation{
				{Kind: facts.RelImports, Target: "missing/module"},
				{Kind: facts.RelDeclares, Target: "pkg.Foo"},
			},
		},
		facts.Fact{Kind: facts.KindSymbol, Name: "pkg.Foo", Repo: "repo", File: "repo/pkg/types.go", Line: 4},
		facts.Fact{Kind: facts.KindModule, Name: "pkg.Foo", Repo: "repo", File: "repo/pkg/Foo"},
	)

	view := buildGraphView(store, "snap-1")
	if view == nil {
		t.Fatal("buildGraphView returned nil")
	}
	if view.SnapshotID != "snap-1" || view.FactCount != 3 {
		t.Fatalf("metadata = %+v, want snapshot snap-1 and three facts", view)
	}
	if len(view.Nodes) != 4 {
		t.Fatalf("nodes = %d, want three facts plus one unresolved target", len(view.Nodes))
	}
	if view.Unresolved != 1 {
		t.Errorf("unresolved = %d, want 1", view.Unresolved)
	}
	seenIDs := map[string]bool{}
	for _, node := range view.Nodes {
		if seenIDs[node.ID] {
			t.Fatalf("duplicate node ID %q", node.ID)
		}
		seenIDs[node.ID] = true
	}
	if len(view.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(view.Edges))
	}
	if got := view.JSON(); got == "{}" {
		t.Fatal("JSON serialization returned fallback object")
	} else {
		var decoded graphView
		if err := json.Unmarshal([]byte(got), &decoded); err != nil {
			t.Fatalf("JSON is invalid: %v", err)
		}
	}
	bootstrap := view.BootstrapJSON()
	if strings.Contains(bootstrap, `"nodes":[`) || !strings.Contains(bootstrap, `"overview_nodes"`) {
		t.Fatalf("bootstrap payload should contain overview data without full nodes: %s", bootstrap)
	}
}

func TestBuildGraphViewOverviewAggregatesModuleRelations(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "a", Repo: "repo", File: "a"},
		facts.Fact{
			Kind: facts.KindModule, Name: "b", Repo: "repo", File: "b",
			Relations: []facts.Relation{
				{Kind: facts.RelImports, Target: "a"},
				{Kind: facts.RelImports, Target: "a"},
			},
		},
	)

	view := buildGraphView(store, "")
	if len(view.OverviewNodes) != 2 {
		t.Fatalf("overview nodes = %d, want 2", len(view.OverviewNodes))
	}
	if len(view.OverviewEdges) != 1 {
		t.Fatalf("overview edges = %d, want one aggregated edge", len(view.OverviewEdges))
	}
	if !view.OverviewEdges[0].Aggregated {
		t.Error("overview edge is not marked aggregated")
	}
	if view.OverviewEdges[0].Count != 2 {
		t.Errorf("overview edge count = %d, want 2", view.OverviewEdges[0].Count)
	}
	for _, node := range view.OverviewNodes {
		if node.Count != 1 || node.Label != node.Name+" (1)" {
			t.Errorf("overview node = %+v, want count 1 and counted label", node)
		}
	}
}

func TestBuildFocusedGraphIsBounded(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "a", Repo: "repo", File: "a", Relations: []facts.Relation{{Kind: facts.RelImports, Target: "b"}}},
		facts.Fact{Kind: facts.KindModule, Name: "b", Repo: "repo", File: "b", Relations: []facts.Relation{{Kind: facts.RelImports, Target: "c"}}},
		facts.Fact{Kind: facts.KindModule, Name: "c", Repo: "repo", File: "c"},
	)
	store.BuildGraph()

	view := buildFocusedGraph(store, "snap", "b", facts.KindModule, "repo", "both", 1, 10)
	if view.SnapshotID != "snap" || view.Scope != "symbols" {
		t.Fatalf("focused metadata = %+v", view)
	}
	if len(view.Nodes) != 3 {
		t.Fatalf("focused nodes = %d, want focus plus two neighbors", len(view.Nodes))
	}
	if len(view.Edges) != 2 {
		t.Fatalf("focused edges = %d, want two", len(view.Edges))
	}
	for _, node := range view.Nodes {
		if node.ID == "" || node.Label == "" {
			t.Errorf("focused node lacks browser identity or label: %+v", node)
		}
	}
}

func TestBuildFocusedGraphIncludesDirectImplements(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "api/domain/fulfilment", Repo: "api", File: "api/domain/fulfilment"},
		facts.Fact{
			Kind: facts.KindSymbol, Name: "api/domain/fulfilment/notions.Fulfilment",
			Repo: "api", File: "api/domain/fulfilment/notions.py",
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: "api/domain/fulfilment"},
				{Kind: facts.RelImplements, Target: "api/infra/db/model.Model"},
				{Kind: facts.RelImplements, Target: "api/domain/fulfilment/notions.RawFulfilment"},
				{Kind: facts.RelImplements, Target: "api/infra/state_machine.StateMachine"},
			},
		},
		facts.Fact{Kind: facts.KindSymbol, Name: "api/domain/fulfilment/notions.RawFulfilment", Repo: "api", File: "api/domain/fulfilment/notions.py"},
		facts.Fact{Kind: facts.KindSymbol, Name: "api/infra/db/model.Model", Repo: "api", File: "api/infra/db/model.py"},
		facts.Fact{Kind: facts.KindSymbol, Name: "api/infra/state_machine.StateMachine", Repo: "api", File: "api/infra/state_machine.py"},
	)
	store.BuildGraph()

	view := buildFocusedGraph(store, "snap", "api/domain/fulfilment", facts.KindModule, "api", "both", 1, 150)
	implements := map[string]bool{}
	for _, edge := range view.Edges {
		if edge.Kind != facts.RelImplements {
			continue
		}
		for _, node := range view.Nodes {
			if node.ID == edge.Target {
				implements[node.Name] = true
			}
		}
	}
	for _, want := range []string{
		"api/infra/db/model.Model",
		"api/domain/fulfilment/notions.RawFulfilment",
		"api/infra/state_machine.StateMachine",
	} {
		if !implements[want] {
			t.Errorf("focused graph missing implements target %q", want)
		}
	}
}

func TestBuildGraphViewRollsUpMemberCalls(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "a", Repo: "repo", File: "a"},
		facts.Fact{
			Kind: facts.KindSymbol, Name: "a.Foo", Repo: "repo", File: "a/foo.go",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "b.Bar"}},
		},
		facts.Fact{Kind: facts.KindModule, Name: "b", Repo: "repo", File: "b"},
		facts.Fact{Kind: facts.KindSymbol, Name: "b.Bar", Repo: "repo", File: "b/bar.go"},
	)
	store.BuildGraph()

	view := buildGraphView(store, "snap")
	var calls *graphEdge
	for i := range view.OverviewEdges {
		if view.OverviewEdges[i].Kind == facts.RelCalls {
			calls = &view.OverviewEdges[i]
			break
		}
	}
	if calls == nil {
		t.Fatal("module overview has no aggregated calls edge")
	}
	if calls.Count != 1 {
		t.Errorf("aggregated calls count = %d, want 1", calls.Count)
	}
}

func TestBuildGraphViewRollsUpPrefixedPythonMemberCalls(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "api/domain/source", Repo: "api", File: "api/domain/source"},
		facts.Fact{Kind: facts.KindModule, Name: "api/domain/target", Repo: "api", File: "api/domain/target"},
		facts.Fact{
			Kind: facts.KindSymbol, Name: "api/domain/source.run", Repo: "api",
			File:      "api/domain/source/run.py",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "api/domain/target.handle"}},
		},
		facts.Fact{
			Kind: facts.KindSymbol, Name: "api/domain/target.handle", Repo: "api",
			File: "api/domain/target/handle.py",
		},
	)
	store.BuildGraph()

	view := buildGraphView(store, "snap")
	for _, edge := range view.OverviewEdges {
		if edge.Kind == facts.RelCalls {
			return
		}
	}
	t.Fatal("module overview has no calls edge for repo-prefixed Python facts")
}

func BenchmarkBuildFocusedGraph(b *testing.B) {
	store := facts.NewStore()
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("module/%04d", i)
		var relations []facts.Relation
		if i > 0 {
			relations = []facts.Relation{{Kind: facts.RelImports, Target: fmt.Sprintf("module/%04d", i-1)}}
		}
		store.Add(facts.Fact{Kind: facts.KindModule, Name: name, Repo: "repo", File: name, Relations: relations})
	}
	store.BuildGraph()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildFocusedGraph(store, "benchmark", "module/0500", facts.KindModule, "repo", "both", 1, 150)
	}
}

func TestHandlerRendersInteractiveGraphPayload(t *testing.T) {
	isolateHome(t)
	store := facts.NewStore()
	store.Add(facts.Fact{
		Kind: facts.KindModule, Name: "pkg", Repo: "repo", File: "pkg",
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: "missing"}},
	})
	s := newTestServer(7171, fakeArtifacts{
		store: store,
		graph: &facts.GraphReceipt{SnapshotID: "graph-snap"},
	})

	rec := httptest.NewRecorder()
	s.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="graph-data"`,
		`Open graph viewer`,
		`graph-snap`,
		`"fact_count":1`,
		`cytoscape@3.31.2`,
		`data-graph-node="module"`,
		`data-graph-edge="imports"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
