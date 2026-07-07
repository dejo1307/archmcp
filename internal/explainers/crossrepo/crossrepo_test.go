package crossrepo

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestExplain_SummarizesCrossRepoEdges(t *testing.T) {
	s := facts.NewStore()
	s.Add(
		facts.Fact{
			Kind: facts.KindDependency, Name: "svc-alpha -> svc-beta", Repo: "svc-alpha",
			Props: map[string]any{
				"type": "cross_repo", "synthetic": "crossrepo",
				"via": []string{"http"}, "endpoint_count": 3,
			},
		},
		facts.Fact{
			Kind: facts.KindDependency, Name: "app-web-app -> app-web", Repo: "app-web-app",
			Props: map[string]any{
				"type": "cross_repo", "synthetic": "crossrepo",
				"via": []string{"import"}, "import_count": 7,
			},
		},
		// A non-cross-repo dependency must be ignored.
		facts.Fact{Kind: facts.KindDependency, Name: "a -> b", Repo: "svc-alpha"},
	)

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want 1", len(insights))
	}
	ins := insights[0]
	if !strings.Contains(ins.Title, "2 edges") {
		t.Errorf("title = %q, want it to mention 2 edges", ins.Title)
	}
	if len(ins.Evidence) != 2 {
		t.Errorf("evidence count = %d, want 2", len(ins.Evidence))
	}
	if !strings.Contains(ins.Description, "svc-alpha -> svc-beta") ||
		!strings.Contains(ins.Description, "3 endpoint") {
		t.Errorf("description missing expected detail: %q", ins.Description)
	}
}

func TestExplain_NoCrossRepoFactsReturnsNil(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindModule, Name: "m"})
	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain error: %v", err)
	}
	if insights != nil {
		t.Errorf("insights = %+v, want nil", insights)
	}
}

// propInt tolerates the float64 form produced by a JSON round-trip.
func TestPropInt_JSONRoundTripForm(t *testing.T) {
	d := facts.Fact{Props: map[string]any{"endpoint_count": float64(5)}}
	if got := propInt(d, "endpoint_count"); got != 5 {
		t.Errorf("propInt(float64 5) = %d, want 5", got)
	}
}

// TestExplain_ViaJSONLRoundTripForm guards BUG-4: a store reloaded from
// facts.jsonl decodes the "via" array as []any, not []string. edgeDetail used to
// type-assert []string only, so the "via ..." clause silently vanished on a
// reloaded snapshot. Both int (float64) and slice ([]any) props must survive.
func TestExplain_ViaJSONLRoundTripForm(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{
		Kind: facts.KindDependency, Name: "svc-a -> svc-b", Repo: "svc-a",
		Props: map[string]any{
			"type": "cross_repo",
			"via":  []any{"http", "import"}, // JSONL round-trip shape
			// endpoint_count as float64, the JSON number shape.
			"endpoint_count": float64(4),
		},
	})

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want 1", len(insights))
	}
	desc := insights[0].Description
	if !strings.Contains(desc, "via http+import") {
		t.Errorf("description missing 'via http+import' (dropped on JSONL form): %q", desc)
	}
	if !strings.Contains(desc, "4 endpoint") {
		t.Errorf("description missing '4 endpoint(s)' from float64 count: %q", desc)
	}
}

// TestExplain_DeterministicSortedByName: edges added out of name order must be
// reported sorted, so insights.json is stable.
func TestExplain_DeterministicSortedByName(t *testing.T) {
	s := facts.NewStore()
	s.Add(
		facts.Fact{Kind: facts.KindDependency, Name: "z-svc -> y-svc", Props: map[string]any{"type": "cross_repo", "via": []string{"http"}}},
		facts.Fact{Kind: facts.KindDependency, Name: "a-svc -> b-svc", Props: map[string]any{"type": "cross_repo", "via": []string{"http"}}},
		facts.Fact{Kind: facts.KindDependency, Name: "m-svc -> n-svc", Props: map[string]any{"type": "cross_repo", "via": []string{"http"}}},
	)
	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	var order []string
	for _, ev := range insights[0].Evidence {
		order = append(order, ev.Fact)
	}
	want := []string{"a-svc -> b-svc", "m-svc -> n-svc", "z-svc -> y-svc"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("evidence order = %v, want sorted %v", order, want)
	}
}

// TestExplain_AllZeroCountsFallback: a cross_repo edge with no via/counts falls
// back to the generic "cross-repo dependency" detail rather than an empty string.
func TestExplain_AllZeroCountsFallback(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindDependency, Name: "svc-a -> svc-b", Props: map[string]any{"type": "cross_repo"}})
	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if !strings.Contains(insights[0].Description, "cross-repo dependency") {
		t.Errorf("expected generic fallback detail, got %q", insights[0].Description)
	}
}
