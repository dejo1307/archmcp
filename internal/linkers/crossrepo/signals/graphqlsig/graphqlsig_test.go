package graphqlsig

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

type fakeInput struct {
	plugin.SignalInput
	facts []facts.Fact
}

func (f fakeInput) Facts() []facts.Fact { return f.facts }

type fakeEdge struct{}

func (fakeEdge) Via(string)                    {}
func (fakeEdge) Confidence(string)             {}
func (fakeEdge) Sample(plugin.Bucket, string)  {}
func (fakeEdge) Unverified(plugin.Bucket, int) {}

type fakeSink struct {
	plugin.EvidenceSink
	edges    int
	coverage map[string]*plugin.Coverage
}

func (s *fakeSink) Edge(consumer, provider string) plugin.EdgeEvidence {
	s.edges++
	return fakeEdge{}
}

func (s *fakeSink) Coverage(repo, edgeType string) *plugin.Coverage {
	if s.coverage == nil {
		s.coverage = map[string]*plugin.Coverage{}
	}
	if s.coverage[repo] == nil {
		s.coverage[repo] = &plugin.Coverage{}
	}
	return s.coverage[repo]
}

func gqlRoute(repo, name, role string) facts.Fact {
	return facts.Fact{Kind: facts.KindRoute, Name: name, Repo: repo,
		Props: map[string]any{"type": facts.RouteTypeGraphQL, "role": role}}
}

// TestSelfMatchCountsResolved pins the internal-consumption rule: a frontend
// querying its own repo's schema is resolved coverage and never an edge — a
// monolith's own operations are not a blind spot.
func TestSelfMatchCountsResolved(t *testing.T) {
	sink := &fakeSink{}
	New().Contribute(fakeInput{facts: []facts.Fact{
		gqlRoute("app", "Query.pageViews", facts.RoleServer),
		gqlRoute("app", "Query.pageViews", facts.RoleClient),
		gqlRoute("mobile", "Query.pageViews", facts.RoleClient),
	}}, sink)
	if c := sink.coverage["app"]; c == nil || c.Detected != 1 || c.Resolved != 1 {
		t.Fatalf("self-consumption must count resolved: %+v", sink.coverage["app"])
	}
	if c := sink.coverage["mobile"]; c == nil || c.Resolved != 1 {
		t.Fatalf("cross-repo consumption must still resolve: %+v", sink.coverage["mobile"])
	}
	if sink.edges != 1 {
		t.Fatalf("only the cross-repo consumption draws an edge, got %d", sink.edges)
	}
}
