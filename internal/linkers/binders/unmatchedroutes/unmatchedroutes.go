// Package unmatchedroutes flags routes the cross-repo linker could not pair up.
package unmatchedroutes

import (
	"context"
	"log"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/crossrepo/routeindex"
	httpsignal "github.com/enola-labs/enola/internal/linkers/crossrepo/signals/http"
	"github.com/enola-labs/enola/internal/linkers/vocab"
	"github.com/enola-labs/enola/pkg/plugin"
)

// Props written by this binder. They are the queryable counterpart to the aggregate
// edge_coverage numbers on a service node: the coverage figure says HOW MANY call
// sites went unresolved, these say WHICH ones and why.
const (
	// PropUnmatchedByClients marks a SERVER route no loaded client calls.
	PropUnmatchedByClients = "unmatched_by_clients"
	// PropUnmatchedByServer marks a CLIENT call site that resolves to no loaded server.
	PropUnmatchedByServer = "unmatched_by_server"
	// PropUnmatchedReason carries one of the crossrepo.Reason* values explaining why.
	PropUnmatchedReason = "unmatched_reason"
)

// Binder records the cross-repo linker's non-matches on the route facts themselves,
// in both directions: a server route no loaded client calls, and a client call site
// that resolves to no loaded server (with the reason it did not).
//
// It is post-link and that is a correctness constraint: it reports the linker's
// verdicts, so running it before linking would report verdicts that had not been
// reached yet.
//
// Writing the verdict as a prop rather than an insight is deliberate. A finding per
// unused endpoint would be noise on a large backend, and the props make the residual
// self-triaging — query_facts(kind=route, prop=unmatched_by_clients) is the whole
// report. It also stays comparable across snapshots for free, since a prop that
// appears or disappears shows up in the snapshot diff.
//
// Both directions CLEAR their props when a route no longer qualifies, which is what
// makes the binder idempotent across appends: loading a second repo can resolve a call
// site that was unmatched when only one repo was in the store, and the stale prop must
// not survive that.
type Binder struct {
	m *routeindex.Matcher
}

// New returns the binder, matching under the given vocabulary.
//
// It takes the vocabulary at construction rather than reading it from the store,
// because its verdicts must be computed with the SAME matching rules the HTTP signal
// used — a binder matching under different vocabulary would report as "unmatched"
// routes the linker had happily linked.
func New(v *vocab.Set) *Binder { return &Binder{m: routeindex.New(v)} }

func (b *Binder) Name() string { return "unmatched-routes" }

func (b *Binder) Stage() plugin.BindStage { return plugin.StagePostLink }

func (b *Binder) Bind(_ context.Context, store *facts.Store) error {
	// FactsRef, not All: both key builders only read, and both finish before the
	// UpdateWhere below mutates anything. A copy here duplicated the entire fact set —
	// including, since Freeze made sharing load-bearing, every Props map and Relations
	// slice — during linking, on every snapshot.
	all := store.FactsRef()
	serverKeys := httpsignal.UnmatchedServerRouteKeys(b.m, all)
	clientKeys := httpsignal.UnmatchedClientRouteKeys(b.m, all)

	flaggedServer, flaggedClient := 0, 0
	store.UpdateWhere(func(f *facts.Fact) {
		if f.Kind != facts.KindRoute {
			return
		}
		// A client-role route is a call site, never a served endpoint: it carries the
		// reverse (unmatched_by_server) verdict, never unmatched_by_clients.
		if f.Props != nil && f.Props[facts.PropRole] == facts.RoleClient {
			delete(f.Props, PropUnmatchedByClients)
			if reason, ok := clientKeys[routeindex.RouteIdentity(*f)]; ok {
				f.Props[PropUnmatchedByServer] = true
				f.Props[PropUnmatchedReason] = reason
				flaggedClient++
			} else {
				delete(f.Props, PropUnmatchedByServer)
				delete(f.Props, PropUnmatchedReason)
			}
			return
		}
		if serverKeys[routeindex.RouteIdentity(*f)] {
			if f.Props == nil {
				f.Props = map[string]any{}
			}
			f.Props[PropUnmatchedByClients] = true
			flaggedServer++
			return
		}
		if f.Props != nil {
			delete(f.Props, PropUnmatchedByClients)
		}
	})
	if flaggedServer > 0 || flaggedClient > 0 {
		log.Printf("[binder:unmatched-routes] flagged %d server route(s) unused by clients, %d client call(s) unresolved to a server",
			flaggedServer, flaggedClient)
	}
	return nil
}
