package engine

import (
	"log"

	"github.com/enola-labs/enola/internal/facts"
)

// bindHTTPHandlers resolves each Go HTTP route to the symbol that actually serves it,
// emitting a handled_by edge — the same contract bindGRPCHandlers provides for gRPC.
//
// WHY IT IS NEEDED. goextractor renders a route's `handler` prop from the REGISTRATION
// site (routes.go, exprToString), so it is the receiver VARIABLE chain as written there:
//
//	handler prop : h.settingsAnalyticsHandlerV2.GetActiveRoles
//	real symbol  : internal/adapters/http/settings/analytics.HandlerV2.GetActiveRoles
//
// The receiver variable is not the receiver type, and the registration site is not the
// declaring package, so the two key spaces are disjoint: on fairwayhub/golf, 1397
// distinct handler props intersect 13482 symbol names in exactly TWO places (both
// Python), and 0 of 1641 routes carried a handled_by edge. Every consumer that keys off
// the handler — the performance analyzer's route-handler escalation, orphans' rescue of
// handler methods — was therefore matching nothing.
//
// WHY IT RESOLVES BY SIGNATURE. The only thing the two sides share is the METHOD NAME,
// and a method name alone is ambiguous. fairwayhub/golf has four GetDailyWeatherRange:
// the real handler, a domain service, a mock, and a null-object stub in the WIRING
// package where the routes happen to be registered — so both a name-only rule and a
// package-scoped rule bind the route to the stub. A wrong handled_by edge feeds
// impact_analysis and find_path, which is worse than the missing edge it replaces.
//
// An HTTP handler is exactly func(http.ResponseWriter, *http.Request). goextractor tags
// those with `http_handler: true` (v111), which is a structural fact from the parser,
// not a name heuristic — and it rejects the stub (signature `(ctx, string)`) by
// construction. Measured on fairwayhub/golf: 1254 of 1639 Go routes resolve, 212 are
// skipped as ambiguous, 16 are unresolved, 154 are middleware.
//
// It runs post-extraction over the assembled store, before BuildGraph, and is
// idempotent — so it recomputes safely on every snapshot and append, with no
// per-extractor cache involvement (and therefore no cacheVersion of its own; the
// http_handler PROP is what needed v111).
func (e *Engine) bindHTTPHandlers() {
	// Index HTTP handler symbols by method name, per repo. A method name claimed by two
	// handlers is poisoned: from the registration site alone there is nothing left to
	// tell them apart, and grpcbind's precedent is to skip rather than guess.
	index := map[string]map[string]string{}   // repo → method name → symbol name
	ambiguous := map[string]map[string]bool{} // repo → method name → seen twice

	for _, s := range e.store.ByKind(facts.KindSymbol) {
		if ok, _ := s.Props["http_handler"].(bool); !ok {
			continue
		}
		method := lastDotSegment(s.Name)
		if method == "" {
			continue
		}
		if index[s.Repo] == nil {
			index[s.Repo] = map[string]string{}
			ambiguous[s.Repo] = map[string]bool{}
		}
		if existing, ok := index[s.Repo][method]; ok && existing != s.Name {
			ambiguous[s.Repo][method] = true
		} else {
			index[s.Repo][method] = s.Name
		}
	}

	bound := 0
	e.store.UpdateWhere(func(f *facts.Fact) {
		if f.Kind != facts.KindRoute || f.Props == nil {
			return
		}
		if lang, _ := f.Props["language"].(string); lang != "go" {
			return
		}
		// Middleware's "handler" is a middleware func, not a (w, r) handler. It carries
		// no http_handler prop so it could not bind anyway, but skip it explicitly:
		// binding middleware to a route would pollute the very edges this exists to make
		// trustworthy.
		if t, _ := f.Props[facts.PropRouteType].(string); t == facts.RouteTypeMiddleware {
			return
		}
		handler := propStr(f, "handler")
		if handler == "" {
			return
		}
		method := lastDotSegment(handler)
		if method == "" || ambiguous[f.Repo][method] {
			return
		}
		target := index[f.Repo][method]
		if target == "" {
			return
		}
		if hasRelation(f, facts.RelHandledBy, target) {
			return // idempotent across appends
		}
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelHandledBy, Target: target})
		// Deliberately NOT overwriting Props["handler"]: unlike grpcbind, which
		// reconstructs an exact FQN, the raw prop here is the literal registration-site
		// expression and is the only record of HOW the route was wired. The resolved
		// symbol lives on the edge.
		bound++
	})
	if bound > 0 {
		log.Printf("[engine] bound %d Go HTTP route(s) to their handler", bound)
	}
}
