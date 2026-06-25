package extractors

import (
	"context"
	"errors"
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

func TestRegistry_DetectAll(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeExtractor{name: "match1", detect: true})
	r.Register(fakeExtractor{name: "nomatch", detect: false})
	r.Register(fakeExtractor{name: "match2", detect: true})

	matched, err := r.DetectAll("/some/repo")
	if err != nil {
		t.Fatalf("DetectAll: %v", err)
	}
	if len(matched) != 2 {
		t.Fatalf("DetectAll matched %d, want 2", len(matched))
	}
	if matched[0].Name() != "match1" || matched[1].Name() != "match2" {
		t.Errorf("DetectAll = [%s %s], want [match1 match2]", matched[0].Name(), matched[1].Name())
	}
}

func TestRegistry_DetectAll_PropagatesError(t *testing.T) {
	r := NewRegistry()
	sentinel := errors.New("detect failed")
	r.Register(fakeExtractor{name: "ok", detect: true})
	r.Register(fakeExtractor{name: "boom", detectErr: sentinel})

	if _, err := r.DetectAll("/repo"); !errors.Is(err, sentinel) {
		t.Errorf("DetectAll error = %v, want %v", err, sentinel)
	}
}
