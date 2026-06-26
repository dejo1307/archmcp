package unusedroutes

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func flaggedRoute(repo, path, method string) facts.Fact {
	return facts.Fact{Kind: facts.KindRoute, Name: path, Repo: repo, Props: map[string]any{
		"method":               method,
		"role":                 "server",
		"unmatched_by_clients": true,
	}}
}

func TestExplain_FlagsCandidatesWithCaveat(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindService, Name: "golf", Repo: "golf"}, // multi-repo marker
		facts.Fact{Kind: facts.KindService, Name: "golf-ui", Repo: "golf-ui"},
		flaggedRoute("golf", "/api/secret/cleanup", "POST"),
		flaggedRoute("golf", "/api/legacy/export", "GET"),
		// A called route (no flag) must not appear.
		facts.Fact{Kind: facts.KindRoute, Name: "/api/items/{id}", Repo: "golf",
			Props: map[string]any{"method": "GET", "role": "server"}},
	)

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 per-repo insight, got %d: %+v", len(insights), insights)
	}
	in := insights[0]
	if !strings.Contains(in.Title, "golf") || !strings.Contains(in.Title, "2 route") {
		t.Errorf("title should name repo and count; got %q", in.Title)
	}
	// The mandatory out-of-snapshot caveat must be present.
	for _, want := range []string{"loaded clients ONLY", "webhooks", "Verify"} {
		if !strings.Contains(in.Description, want) {
			t.Errorf("description missing caveat phrase %q; got %q", want, in.Description)
		}
	}
	if strings.Contains(in.Description, "/api/items/{id}") {
		t.Errorf("a called (unflagged) route leaked into the insight: %q", in.Description)
	}
	if in.Confidence >= 0.9 {
		t.Errorf("confidence %v too high for a candidate-with-caveat signal", in.Confidence)
	}
}

func TestExplain_SingleRepoNoServiceNodesYieldsNothing(t *testing.T) {
	store := facts.NewStore()
	// Route flagged but no service nodes (single-repo snapshot) -> no insights.
	store.Add(flaggedRoute("golf", "/api/secret/cleanup", "POST"))

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("single-repo snapshot should yield no insights, got %d", len(insights))
	}
}
