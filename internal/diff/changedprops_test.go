package diff

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func factWithProps(name string, props map[string]any) facts.Fact {
	return facts.Fact{Kind: facts.KindModule, Name: name, File: name, Props: props}
}

func TestChangedProps(t *testing.T) {
	tests := []struct {
		name   string
		before map[string]any
		after  map[string]any
		want   []string
	}{
		{
			name:   "identical props report nothing",
			before: map[string]any{"instability": 0.5},
			after:  map[string]any{"instability": 0.5},
		},
		{
			name:   "a moved value reads as before to after",
			before: map[string]any{"instability": 0.3},
			after:  map[string]any{"instability": 0.72},
			want:   []string{"instability: 0.3 → 0.72"},
		},
		{
			name:   "a newly-set prop is distinguished from a changed one",
			before: map[string]any{},
			after:  map[string]any{"distance": 0.86},
			want:   []string{"distance: (unset) → 0.86"},
		},
		{
			name:   "a dropped prop is reported rather than silently ignored",
			before: map[string]any{"distance": 0.86},
			after:  map[string]any{},
			want:   []string{"distance: 0.86 → (unset)"},
		},
		{
			name:   "only the props that moved are listed",
			before: map[string]any{"afferent": 3, "efferent": 4, "instability": 0.57},
			after:  map[string]any{"afferent": 3, "efferent": 9, "instability": 0.75},
			want:   []string{"efferent: 4 → 9", "instability: 0.57 → 0.75"},
		},
		{
			name:   "non-numeric props are handled, not assumed numeric",
			before: map[string]any{"symbol_kind": "function"},
			after:  map[string]any{"symbol_kind": "method"},
			want:   []string{"symbol_kind: function → method"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := changedProps(FactChange{
				Before: factWithProps("m", tt.before),
				After:  factWithProps("m", tt.after),
			})
			if len(got) != len(tt.want) {
				t.Fatalf("changedProps() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Output must not reshuffle between runs. Props are a map, and a renderer whose order
// follows map iteration makes two identical diffs look different — the same class of
// defect that once made insights.json non-reproducible.
func TestChangedPropsIsOrderStable(t *testing.T) {
	before := map[string]any{"afferent": 1, "efferent": 1, "instability": 0.1, "abstractness": 0.1, "distance": 0.1}
	after := map[string]any{"afferent": 2, "efferent": 2, "instability": 0.2, "abstractness": 0.2, "distance": 0.2}

	first := changedProps(FactChange{Before: factWithProps("m", before), After: factWithProps("m", after)})
	for i := 0; i < 50; i++ {
		got := changedProps(FactChange{Before: factWithProps("m", before), After: factWithProps("m", after)})
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d differs at line %d: %q vs %q", i, j, got[j], first[j])
			}
		}
	}
	if len(first) != 5 {
		t.Errorf("got %d changed props, want 5: %v", len(first), first)
	}
}

// The delta has to reach the rendered diff, not only the JSON. A changed fact used to
// render as a bare line, so the reader could see THAT something moved but not what.
func TestRenderNamesTheChangedProp(t *testing.T) {
	d := &SnapshotDiff{FactsChanged: []FactChange{{
		Before: factWithProps("internal/foo", map[string]any{"instability": 0.30}),
		After:  factWithProps("internal/foo", map[string]any{"instability": 0.72}),
	}}}

	out := d.RenderCompact()

	if !strings.Contains(out, "internal/foo") {
		t.Fatalf("changed fact missing from the diff:\n%s", out)
	}
	if !strings.Contains(out, "instability: 0.3 → 0.72") {
		t.Errorf("the diff does not say WHAT moved:\n%s", out)
	}
}
