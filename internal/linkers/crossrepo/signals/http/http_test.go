package http

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/crossrepo/routeindex"
	"github.com/enola-labs/enola/internal/linkers/vocab"
	"github.com/enola-labs/enola/pkg/plugin"
)

type fakeInput struct {
	plugin.SignalInput
	facts []facts.Fact
}

func (f fakeInput) Facts() []facts.Fact { return f.facts }

// ResolveRepo mirrors the real SignalInput over a fact set: a candidate
// resolves when its normalized form equals a loaded repo label's. It lives here
// because only this fake needs it — production readers have in.ResolveRepo.
func (f fakeInput) ResolveRepo(candidate string) (string, bool) {
	want := facts.NormalizeRepoLabel(candidate)
	if want == "" {
		return "", false
	}
	for r := range reposOf(f.facts) {
		if facts.NormalizeRepoLabel(r) == want {
			return r, true
		}
	}
	return "", false
}

type recordedEdge struct{ from, to string }

type fakeEdge struct{}

func (fakeEdge) Via(string)                    {}
func (fakeEdge) Confidence(string)             {}
func (fakeEdge) Sample(plugin.Bucket, string)  {}
func (fakeEdge) Unverified(plugin.Bucket, int) {}

type fakeSink struct {
	plugin.EvidenceSink
	edges []recordedEdge
}

func (s *fakeSink) Edge(consumer, provider string) plugin.EdgeEvidence {
	s.edges = append(s.edges, recordedEdge{consumer, provider})
	return fakeEdge{}
}

func (s *fakeSink) Coverage(string, string) *plugin.Coverage { return &plugin.Coverage{} }

func runSignal(t *testing.T, ff ...facts.Fact) []recordedEdge {
	t.Helper()
	sink := &fakeSink{}
	New(vocab.Default()).Contribute(fakeInput{facts: ff}, sink)
	return sink.edges
}

// TestSingleSegmentPath_ExactHintCarveOut pins the one exception to the
// single-segment demand for an outright unambiguous provider: a hint whose
// normalized form EQUALS a provider's label (the source names the host —
// `${config.BACKEND_HOST}/mcp`) disambiguates; a substring hint does not.
func TestSingleSegmentPath_ExactHintCarveOut(t *testing.T) {
	client := facts.Fact{Kind: facts.KindRoute, Name: "/mcp", Repo: "agent",
		Props: map[string]any{"role": "client", "method": facts.MethodAny,
			"source": facts.RouteSourceTSHTTPClient, "target_hint": "backend"}}
	serverA := facts.Fact{Kind: facts.KindRoute, Name: "/mcp", Repo: "backend",
		Props: map[string]any{"method": "POST"}}
	serverB := facts.Fact{Kind: facts.KindRoute, Name: "/mcp", Repo: "other",
		Props: map[string]any{"method": "POST"}}
	edges := runSignal(t, client, serverA, serverB)
	if len(edges) != 1 || edges[0].to != "backend" {
		t.Fatalf("an exact-label hint must disambiguate a single-segment path: %+v", edges)
	}
	client.Props["target_hint"] = "back"
	if edges := runSignal(t, client, serverA, serverB); len(edges) != 0 {
		t.Fatalf("a substring hint must stay rejected for single-segment paths: %+v", edges)
	}
}

// The carve-out reads target_hint and nothing else. serviceHint falls back to
// the `api` prop, which is the client FILE's name: a file named api.ts would
// otherwise elect the repo named api out of several candidates, and renaming
// that file would move the dependency.
func TestSingleSegmentPath_FileNameIsNotAHint(t *testing.T) {
	client := facts.Fact{Kind: facts.KindRoute, Name: "/mcp", Repo: "agent",
		Props: map[string]any{"role": "client", "method": facts.MethodAny,
			"source": facts.RouteSourceTSHTTPClient, "api": "backend"}}
	serverA := facts.Fact{Kind: facts.KindRoute, Name: "/mcp", Repo: "backend",
		Props: map[string]any{"method": "POST"}}
	serverB := facts.Fact{Kind: facts.KindRoute, Name: "/mcp", Repo: "other",
		Props: map[string]any{"method": "POST"}}
	if edges := runSignal(t, client, serverA, serverB); len(edges) != 0 {
		t.Fatalf("a file name must not disambiguate a single-segment path: %+v", edges)
	}
}

// A hint that resolves to no loaded repo is not evidence the call leaves the
// estate: it is equally consistent with a derivation that named no provider.
// Externality is claimed from the URL literal, so a hinted non-match stays in
// the residue and is triaged like any other — here by the repo's sole declared
// seam. Omitting it instead deleted real blind spots from the report.
func TestUnmatched_HintDoesNotProveExternalAndIntentAttribution(t *testing.T) {
	hinted := facts.Fact{Kind: facts.KindRoute, Name: "/collections/x", Repo: "app",
		Props: map[string]any{"role": "client", "method": "GET",
			"source": facts.RouteSourceRubyHTTPClient, "target_hint": "fastlyapikey"}}
	declared := facts.Fact{Kind: facts.KindRoute, Name: "/activities", Repo: "app",
		Props: map[string]any{"role": "client", "method": "GET",
			"source": facts.RouteSourceTSHTTPClient}}
	intent := facts.Fact{Kind: facts.KindIntent, Repo: "app", Name: "consumes backend via http-client",
		Props: map[string]any{"intent_kind": "consumes", "target": "backend", "via": "http-client"}}
	server := facts.Fact{Kind: facts.KindRoute, Name: "/api/v1/widgets/list", Repo: "backend",
		Props: map[string]any{"method": "GET"}}
	gql := facts.Fact{Kind: facts.KindRoute, Name: "Query.pageViews", Repo: "app",
		Props: map[string]any{"role": "client", "type": "graphql", "source": facts.RouteSourceGraphQLTag}}

	m := routeindex.New(vocab.Default())
	un := UnmatchedClientRouteKeys(m, []facts.Fact{hinted, declared, intent, server, gql})
	for id, reason := range un {
		switch {
		case strings.Contains(id, "/collections/x"):
			if reason != ReasonDeclaredTarget {
				t.Errorf("an unresolvable hint must not omit the call site; want %q, got %q", ReasonDeclaredTarget, reason)
			}
		case strings.Contains(id, "/activities"):
			if reason != ReasonDeclaredTarget {
				t.Errorf("declared-target attribution expected, got %q", reason)
			}
		case strings.Contains(id, "pageViews"):
			t.Errorf("graphql op must be the graphql signal's domain, got reason %q", reason)
		}
	}
	if _, ok := un["app\x00GET\x00/activities"]; !ok {
		found := false
		for id, reason := range un {
			if strings.Contains(id, "/activities") && reason == ReasonDeclaredTarget {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected an attributed_by_intent entry for /activities, got %v", un)
		}
	}
}

// Attribution is stated, not inferred: a repo declaring two http-client seams
// has no single target to attribute an unmatched call to, so the call stays in
// the residue with an ordinary reason rather than being credited to whichever
// declaration was read last.
func TestUnmatched_SeveralDeclaredSeamsAttributeNothing(t *testing.T) {
	call := facts.Fact{Kind: facts.KindRoute, Name: "/activities/recent", Repo: "app",
		Props: map[string]any{"role": "client", "method": "GET",
			"source": facts.RouteSourceTSHTTPClient}}
	one := facts.Fact{Kind: facts.KindIntent, Repo: "app", Name: "consumes backend via http-client",
		Props: map[string]any{"intent_kind": "consumes", "target": "backend", "via": "http-client"}}
	two := facts.Fact{Kind: facts.KindIntent, Repo: "app", Name: "consumes billing via http-client",
		Props: map[string]any{"intent_kind": "consumes", "target": "billing", "via": "http-client"}}
	server := facts.Fact{Kind: facts.KindRoute, Name: "/api/v1/widgets/list", Repo: "backend",
		Props: map[string]any{"method": "GET"}}

	m := routeindex.New(vocab.Default())
	un := UnmatchedClientRouteKeys(m, []facts.Fact{call, one, two, server})
	for id, reason := range un {
		if strings.Contains(id, "/activities/recent") && reason == ReasonDeclaredTarget {
			t.Fatalf("two declared seams must attribute nothing, got %q", reason)
		}
	}
}
