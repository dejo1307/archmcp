// Package graphqlsig links repos coupled by GraphQL operations.
package graphqlsig

import (
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

// Signal binds a client operation's root field to the loaded schema that serves
// it — the HTTP linker's client→server shape applied to GraphQL, joined on the
// exact `Kind.fieldName` names both extractors emit. Ambiguity skips: the same
// root field served by two loaded schemas means the name alone cannot decide,
// exactly the grpcimpl precedent.
type Signal struct{}

// New returns the signal.
func New() *Signal { return &Signal{} }

func (s *Signal) Name() string { return "graphql" }

func (s *Signal) Phase() plugin.SignalPhase { return plugin.PhaseDirectional }

// CoverageEdgeType is the edge class this signal reports coverage under.
const CoverageEdgeType = "graphql_client"

func (s *Signal) Contribute(in plugin.SignalInput, out plugin.EvidenceSink) {
	servers := map[string]string{}
	ambiguous := map[string]bool{}
	for _, f := range in.Facts() {
		if f.Kind != facts.KindRoute || f.Repo == "" ||
			f.PropString(facts.PropRouteType) != facts.RouteTypeGraphQL ||
			f.PropString(facts.PropRole) != facts.RoleServer {
			continue
		}
		if owner, taken := servers[f.Name]; taken && owner != f.Repo {
			ambiguous[f.Name] = true
			continue
		}
		servers[f.Name] = f.Repo
	}
	for _, f := range in.Facts() {
		if f.Kind != facts.KindRoute || f.Repo == "" ||
			f.PropString(facts.PropRouteType) != facts.RouteTypeGraphQL ||
			f.PropString(facts.PropRole) != facts.RoleClient {
			continue
		}
		cov := out.Coverage(f.Repo, CoverageEdgeType)
		cov.Detected++
		server, ok := servers[f.Name]
		if !ok || ambiguous[f.Name] || server == f.Repo {
			continue
		}
		cov.Resolved++
		e := out.Edge(f.Repo, server)
		e.Via("graphql")
		e.Sample(plugin.BucketEndpoints, f.Name)
	}
}
