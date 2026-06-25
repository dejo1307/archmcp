package renderers

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

type fakeRenderer struct{ name string }

func (f fakeRenderer) Name() string { return f.name }
func (f fakeRenderer) Render(context.Context, *facts.Snapshot) ([]facts.Artifact, error) {
	return nil, nil
}

func TestRegistry_RegisterGetAll(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeRenderer{name: "llm_context"})
	r.Register(fakeRenderer{name: "graphviz"})

	if got := r.Get("llm_context"); got == nil || got.Name() != "llm_context" {
		t.Errorf("Get(llm_context) = %v, want llm_context renderer", got)
	}
	if got := r.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
	if all[0].Name() != "llm_context" || all[1].Name() != "graphviz" {
		t.Errorf("All() order = [%s %s], want [llm_context graphviz]", all[0].Name(), all[1].Name())
	}
}
