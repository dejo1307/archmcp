package unusedroutes

import (
	"context"
	"fmt"
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

func TestExplain_CapsSamplesAndEvidence(t *testing.T) {
	store := facts.NewStore()
	store.Add(facts.Fact{Kind: facts.KindService, Name: "golf", Repo: "golf"})
	const n = 30 // > maxSamples (25)
	for i := 0; i < n; i++ {
		store.Add(flaggedRoute("golf", fmt.Sprintf("/api/ep%02d", i), "POST"))
	}

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	in := insights[0]
	// The title counts ALL flagged routes, not just the sampled ones.
	if !strings.Contains(in.Title, fmt.Sprintf("%d route", n)) {
		t.Errorf("title should report full count %d, got %q", n, in.Title)
	}
	// Samples are truncated with a "+N more" marker.
	if !strings.Contains(in.Description, "(+5 more)") {
		t.Errorf("description should note 5 elided samples, got %q", in.Description)
	}
	// Evidence is capped at maxSamples.
	if len(in.Evidence) != maxSamples {
		t.Errorf("evidence should be capped at %d, got %d", maxSamples, len(in.Evidence))
	}
}

func TestExplain_MultipleReposSortedAndRoutesSorted(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindService, Name: "zulu", Repo: "zulu"},
		facts.Fact{Kind: facts.KindService, Name: "alpha", Repo: "alpha"},
		flaggedRoute("zulu", "/api/z2", "GET"),
		flaggedRoute("zulu", "/api/z1", "GET"),
		flaggedRoute("alpha", "/api/a1", "POST"),
	)

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 2 {
		t.Fatalf("expected one insight per repo, got %d", len(insights))
	}
	// Repos are reported in sorted order: alpha before zulu.
	if !strings.Contains(insights[0].Title, "alpha") || !strings.Contains(insights[1].Title, "zulu") {
		t.Errorf("repos not sorted: %q then %q", insights[0].Title, insights[1].Title)
	}
	// Routes within a repo are sorted: z1 before z2.
	zulu := insights[1]
	if len(zulu.Evidence) != 2 || zulu.Evidence[0].Fact != "GET /api/z1" || zulu.Evidence[1].Fact != "GET /api/z2" {
		t.Errorf("routes within repo not sorted: %+v", zulu.Evidence)
	}
}

func TestExplain_NoMethodFallbackAndUnlabeledRepo(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindService, Name: "svc", Repo: "svc"},
		// No Repo label -> "(unlabeled)" bucket; no method prop -> label is the bare name.
		facts.Fact{Kind: facts.KindRoute, Name: "/api/loose", Props: map[string]any{"unmatched_by_clients": true}},
	)

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if !strings.Contains(insights[0].Title, "(unlabeled)") {
		t.Errorf("blank repo should bucket under (unlabeled), got %q", insights[0].Title)
	}
	if insights[0].Evidence[0].Fact != "/api/loose" {
		t.Errorf("route with no method should fall back to its name, got %q", insights[0].Evidence[0].Fact)
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
