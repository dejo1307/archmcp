package dashboard

import (
	"fmt"
	"testing"

	"github.com/enola-labs/enola/pkg/facts"
)

func TestBuildModuleGraphRanksBoundsAndLinks(t *testing.T) {
	st := facts.NewStore()
	st.Add(
		facts.Fact{Kind: facts.KindModule, Name: "core", File: "src/core", Relations: []facts.Relation{{Kind: facts.RelImports, Target: "api"}, {Kind: facts.RelImports, Target: "storage"}}},
		facts.Fact{Kind: facts.KindModule, Name: "api", File: "src/api", Relations: []facts.Relation{{Kind: facts.RelImports, Target: "core"}}},
		facts.Fact{Kind: facts.KindModule, Name: "storage", File: "src/storage"},
		facts.Fact{Kind: facts.KindModule, Name: "core_test", File: "tests/core", Props: map[string]any{facts.PropModuleRole: facts.ModuleRoleTest}, Relations: []facts.Relation{{Kind: facts.RelImports, Target: "core"}}},
	)

	view := buildModuleGraph(st)
	if view == nil || len(view.Nodes) != 3 || len(view.Edges) != 3 {
		t.Fatalf("view = %+v, want 3 production nodes and 3 directed edges", view)
	}
	if view.Nodes[0].Name != "core" || view.Nodes[0].FanIn != 1 || view.Nodes[0].FanOut != 2 {
		t.Errorf("top node = %+v, want core with fan-in 1 / fan-out 2", view.Nodes[0])
	}
}

func TestBuildModuleGraphCapsLargeRepositories(t *testing.T) {
	st := facts.NewStore()
	for i := 0; i < moduleGraphLimit+10; i++ {
		name := fmt.Sprintf("m%02d", i)
		target := fmt.Sprintf("m%02d", (i+1)%(moduleGraphLimit+10))
		st.Add(facts.Fact{Kind: facts.KindModule, Name: name, Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}}})
	}
	view := buildModuleGraph(st)
	if view == nil || len(view.Nodes) != moduleGraphLimit || !view.Limited || view.Total != moduleGraphLimit+10 {
		t.Fatalf("view = %+v, want %d of %d modules", view, moduleGraphLimit, moduleGraphLimit+10)
	}
}
