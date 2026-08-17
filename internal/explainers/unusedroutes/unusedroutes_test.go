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
		// A called route must not appear in the list, but must still count in the
		// denominator — it carries the positive marker, which is what says the
		// linker looked at it and found a caller. A route with neither marker is
		// one the linker declined to evaluate (a UI route, a GraphQL operation, a
		// generic path) and is excluded from both, because a proportion whose
		// denominator includes unevaluated routes describes a population the
		// numerator was never drawn from.
		facts.Fact{Kind: facts.KindRoute, Name: "/api/items/{id}", Repo: "golf",
			Props: map[string]any{"method": "GET", "role": "server",
				"matched_by_clients": true}},
		// Declined by the linker: present, served, and correctly outside the ratio.
		facts.Fact{Kind: facts.KindRoute, Name: "/health", Repo: "golf",
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
	if !strings.Contains(in.Title, "golf") || !strings.Contains(in.Title, "2 of 3 route") {
		t.Errorf("title should name repo, unmatched count and total; got %q", in.Title)
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
	// Enough called routes that the unmatched share stays below the threshold at
	// which the finding is about the client set rather than the endpoints. This
	// test is about sample capping, and it must keep exercising that branch.
	// They carry the positive marker: that is what says the linker evaluated
	// them and found a caller, and only evaluated routes belong in the
	// denominator.
	for i := 0; i < 20; i++ {
		store.Add(facts.Fact{Kind: facts.KindRoute, Name: fmt.Sprintf("/api/called%02d", i), Repo: "golf",
			Props: map[string]any{"method": "GET", "role": "server",
				"matched_by_clients": true}})
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
	if !strings.Contains(in.Title, fmt.Sprintf("%d of 50 route", n)) {
		t.Errorf("title should report full count %d and the total, got %q", n, in.Title)
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
	// Called routes keep each repo's unmatched share below the point at which the
	// finding becomes one about the client set. This test is about ordering and
	// evidence keying, and it has to keep exercising that branch.
	for i := 0; i < 4; i++ {
		store.Add(
			facts.Fact{Kind: facts.KindRoute, Name: fmt.Sprintf("/api/zc%d", i), Repo: "zulu",
				Props: map[string]any{"method": "GET", "role": "server",
					"matched_by_clients": true}},
			facts.Fact{Kind: facts.KindRoute, Name: fmt.Sprintf("/api/ac%d", i), Repo: "alpha",
				Props: map[string]any{"method": "GET", "role": "server",
					"matched_by_clients": true}},
		)
	}

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
	// Routes within a repo are sorted: z1 before z2. Evidence is keyed on the route
	// FACT NAME, not the "METHOD /path" label — that is what lets the snapshot diff
	// attribute a newly-unused route to the change that caused it. The method rides
	// in the detail so two verbs on one path stay distinguishable.
	zulu := insights[1]
	if len(zulu.Evidence) != 2 || zulu.Evidence[0].Fact != "/api/z1" || zulu.Evidence[1].Fact != "/api/z2" {
		t.Errorf("routes within repo not sorted, or not keyed on the fact name: %+v", zulu.Evidence)
	}
	if !strings.HasPrefix(zulu.Evidence[0].Detail, "GET /api/z1 — ") {
		t.Errorf("method should be preserved in the detail, got %q", zulu.Evidence[0].Detail)
	}
}

func TestExplain_NoMethodFallbackAndUnlabeledRepo(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindService, Name: "svc", Repo: "svc"},
		// No Repo label -> "(unlabeled)" bucket; no method prop -> label is the bare name.
		facts.Fact{Kind: facts.KindRoute, Name: "/api/loose", Props: map[string]any{"unmatched_by_clients": true}},
		// Called routes in the same bucket, so the unmatched share stays low
		// enough that this stays a candidate list. The test is about the
		// unlabeled-repo fallback and the missing-method label.
		facts.Fact{Kind: facts.KindRoute, Name: "/api/called1",
			Props: map[string]any{"method": "GET", "matched_by_clients": true}},
		facts.Fact{Kind: facts.KindRoute, Name: "/api/called2",
			Props: map[string]any{"method": "GET", "matched_by_clients": true}},
		facts.Fact{Kind: facts.KindRoute, Name: "/api/called3",
			Props: map[string]any{"method": "GET", "matched_by_clients": true}},
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

// TestExplain_ThinClientCoverageDescribesTheSnapshot pins the case the estate
// actually hit: a large Rails monolith reported 3,392 of 3,732 routes unmatched,
// because its callers are browsers and third-party integrators rather than
// repositories.
// Presenting that as a list of dead-endpoint candidates is how a finding earns
// being ignored wholesale.
func TestExplain_ThinClientCoverageDescribesTheSnapshot(t *testing.T) {
	store := facts.NewStore()
	store.Add(facts.Fact{Kind: facts.KindService, Name: "public-api", Repo: "public-api"})
	for i := 0; i < 90; i++ {
		store.Add(flaggedRoute("public-api", fmt.Sprintf("/v1/ep%02d", i), "GET"))
	}
	for i := 0; i < 10; i++ {
		store.Add(facts.Fact{Kind: facts.KindRoute, Name: fmt.Sprintf("/v1/called%02d", i), Repo: "public-api",
			Props: map[string]any{"method": "GET", "role": "server"}})
	}

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	in := insights[0]
	if !strings.Contains(in.Title, "too thin") {
		t.Errorf("the finding must be about the client set, not the endpoints; got %q", in.Title)
	}
	if len(in.Evidence) != 0 {
		t.Errorf("listing 90 endpoints as candidates is the thing this branch exists to avoid; got %d", len(in.Evidence))
	}
	if in.Confidence < 0.8 {
		t.Errorf("the claim about coverage is a confident one: %v", in.Confidence)
	}
}

// The count of routes nothing assessed is part of the finding, not a detail of
// the implementation. "27 of 39 unused" and "27 of 39, with 827 more the pass
// could not look at" describe very different repositories, and only the second
// one is true of `insights`.
func TestExplain_ReportsWhatWasNotAssessed(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindService, Name: "svc", Repo: "svc"},
		flaggedRoute("svc", "/api/orders", "GET"),
		facts.Fact{Kind: facts.KindRoute, Name: "/api/items", Repo: "svc",
			Props: map[string]any{"method": "GET", "role": "server", "matched_by_clients": true}},
		// Neither marker: the linker declined to reason about these.
		facts.Fact{Kind: facts.KindRoute, Name: "/health", Repo: "svc",
			Props: map[string]any{"method": "GET", "role": "server"}},
		facts.Fact{Kind: facts.KindRoute, Name: "/dashboard", Repo: "svc",
			Props: map[string]any{"method": "GET", "role": "server", "type": "page"}},
	)

	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if !strings.Contains(insights[0].Title, "1 of 2 route(s)") {
		t.Errorf("the denominator must be the routes actually assessed, got %q", insights[0].Title)
	}
	if !strings.Contains(insights[0].Description, "A further 2 route(s)") {
		t.Errorf("the unassessed count must appear in the finding, got %q", insights[0].Description)
	}
}
