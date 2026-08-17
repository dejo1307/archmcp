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
		if !ok || ambiguous[f.Name] {
			continue
		}
		// A self-match — a frontend querying its own repo's schema — is internal
		// consumption: resolved, never a blind spot, and never an edge.
		if server == f.Repo {
			cov.Resolved++
			continue
		}
		cov.Resolved++
		e := out.Edge(f.Repo, server)
		e.Via(facts.ViaGraphQL)
		e.Sample(plugin.BucketEndpoints, f.Name)
	}
}

// ServerOperationVerdicts is the server-side inverse of the join above: which
// declared root operations does no loaded client select?
//
// It exists because the signal computed that answer and threw it away. The
// coverage counters it does keep are per client repo — how many of a client's
// operations resolved — so nothing recorded whether a declared operation is
// consumed, and 201 operations across the estate carried no verdict at all.
// From the facts, a surface nobody rates and a surface nobody can rate looked
// identical.
//
// Two sets, and the second is not derivable from the first, for the same reason
// the HTTP pass returns two: a schema no loaded client selects anything from is
// not a schema of unused operations, it is a schema nobody asked about. Those
// repos appear in neither set.
//
// The join is the identical one Contribute uses — exact `Kind.fieldName`, the
// name both extractors emit — so a verdict here cannot disagree with an edge
// there. Ambiguity is deliberately NOT excluded: a root field two loaded
// schemas both declare is one the client set demonstrably uses, and the
// question here is consumption rather than attribution.
func ServerOperationVerdicts(all []facts.Fact) (evaluated, unmatched map[string]bool) {
	declared := map[string][]string{}
	for _, f := range all {
		if !isGraphQLRoute(f, facts.RoleServer) {
			continue
		}
		declared[f.Repo] = append(declared[f.Repo], f.Name)
	}
	if len(declared) == 0 {
		return nil, nil
	}

	selected := map[string]bool{}
	for _, f := range all {
		if isGraphQLRoute(f, facts.RoleClient) {
			selected[f.Name] = true
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}

	evaluated, unmatched = map[string]bool{}, map[string]bool{}
	for repo, names := range declared {
		consumed := false
		for _, name := range names {
			if selected[name] {
				consumed = true
				break
			}
		}
		// A schema nothing selects from is out of scope entirely, the same way
		// a repo that serves no cross-repo HTTP client is: every operation would
		// be "unused", which describes the snapshot rather than the schema.
		if !consumed {
			continue
		}
		for _, name := range names {
			key := operationIdentity(repo, name)
			evaluated[key] = true
			if !selected[name] {
				unmatched[key] = true
			}
		}
	}
	return evaluated, unmatched
}

// OperationIdentity keys a declared operation for the binder. Unlike an HTTP
// route identity it needs no method, and unlike one it cannot collide with a
// route of another shape: the name carries its own Query/Mutation prefix.
func OperationIdentity(f facts.Fact) string { return operationIdentity(f.Repo, f.Name) }

func operationIdentity(repo, name string) string { return repo + "\x00" + name }

func isGraphQLRoute(f facts.Fact, role string) bool {
	return f.Kind == facts.KindRoute && f.Repo != "" &&
		f.PropString(facts.PropRouteType) == facts.RouteTypeGraphQL &&
		f.PropString(facts.PropRole) == role
}
