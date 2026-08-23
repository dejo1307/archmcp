package extractors

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// fakeExtractor is a configurable plugin.Extractor for registry tests.
type fakeExtractor struct {
	name      string
	detect    bool
	detectErr error
}

func (f fakeExtractor) Name() string { return f.name }
func (f fakeExtractor) Detect(string) (bool, error) {
	return f.detect, f.detectErr
}
func (f fakeExtractor) Extract(context.Context, string, []string) ([]facts.Fact, error) {
	return nil, nil
}

func TestRegistry_RegisterGet(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeExtractor{name: "go"})
	r.Register(fakeExtractor{name: "python"})

	if got := r.Get("python"); got == nil || got.Name() != "python" {
		t.Errorf("Get(python) = %v, want the python extractor", got)
	}
	if got := r.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

func TestRegistry_AllPreservesOrder(t *testing.T) {
	r := NewRegistry()
	names := []string{"cpp", "go", "java", "openapi"}
	for _, n := range names {
		r.Register(fakeExtractor{name: n})
	}

	all := r.All()
	if len(all) != len(names) {
		t.Fatalf("All() len = %d, want %d", len(all), len(names))
	}
	for i, n := range names {
		if all[i].Name() != n {
			t.Errorf("All()[%d] = %q, want %q (insertion order not preserved)", i, all[i].Name(), n)
		}
	}
}
