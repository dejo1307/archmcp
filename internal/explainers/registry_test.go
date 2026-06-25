package explainers

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

type fakeExplainer struct{ name string }

func (f fakeExplainer) Name() string { return f.name }
func (f fakeExplainer) Explain(context.Context, *facts.Store) ([]facts.Insight, error) {
	return nil, nil
}

func TestRegistry_RegisterGetAll(t *testing.T) {
	r := NewRegistry()
	names := []string{"cycles", "layers", "hotspots"}
	for _, n := range names {
		r.Register(fakeExplainer{name: n})
	}

	if got := r.Get("layers"); got == nil || got.Name() != "layers" {
		t.Errorf("Get(layers) = %v, want layers explainer", got)
	}
	if got := r.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}

	all := r.All()
	if len(all) != len(names) {
		t.Fatalf("All() len = %d, want %d", len(all), len(names))
	}
	for i, n := range names {
		if all[i].Name() != n {
			t.Errorf("All()[%d] = %q, want %q", i, all[i].Name(), n)
		}
	}
}
