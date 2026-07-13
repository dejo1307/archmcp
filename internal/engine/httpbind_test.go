package engine

import (
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/facts"
)

// httpRoute builds an HTTP route fact like goextractor's routes.go emits: the `handler`
// prop is rendered from the REGISTRATION site by exprToString, so it is the receiver
// VARIABLE chain, not the symbol's name.
func httpRoute(path, handler string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindRoute,
		Name: path,
		Props: map[string]any{
			"method": "GET", "framework": "gorilla/mux", "language": "go",
			"handler": handler,
		},
	}
}

func goHandler(name string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindSymbol, Name: name,
		Props: map[string]any{
			"symbol_kind": facts.SymbolMethod, "language": "go", "http_handler": true,
		},
	}
}

func goNonHandler(name string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindSymbol, Name: name,
		Props: map[string]any{"symbol_kind": facts.SymbolMethod, "language": "go"},
	}
}

// TestBindHTTPHandlers_BindsViaSignature is new/18. A route's handler prop names the
// receiver VARIABLE ("h.weatherHandler.GetDailyWeatherRange"); the symbol is named by
// its receiver TYPE. The two key spaces are disjoint — on fairwayhub/golf, 1397 handler
// props intersect 13482 symbol names exactly twice, and 0 of 1641 routes carried a
// handled_by edge — so the escalation that keys off them never fired.
func TestBindHTTPHandlers_BindsViaSignature(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		httpRoute("/api/weather/daily-range", "h.weatherHandler.GetDailyWeatherRange"),
		goHandler("internal/adapters/http/weather.HandlerV2.GetDailyWeatherRange"),
	)

	eng.bindHTTPHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/api/weather/daily-range"})
	target, ok := routeHandledBy(routes[0])
	if !ok {
		t.Fatal("route was not bound to its handler")
	}
	if want := "internal/adapters/http/weather.HandlerV2.GetDailyWeatherRange"; target != want {
		t.Errorf("bound to %q, want %q", target, want)
	}
}

// TestBindHTTPHandlers_RejectsNonHandlerBySignature is the mis-bind that killed the
// naive rule, pinned. On fairwayhub/golf, /api/weather/daily-range's handler shares its
// method name with a stub in the WIRING package (internal/bootstrap), and a
// package-scoped or name-only binder resolves the route to that stub:
//
//	binds to : internal/bootstrap.NullWeatherService.GetDailyWeatherRange   ← WRONG
//	truth    : internal/adapters/http/weather.HandlerV2.GetDailyWeatherRange
//
// A wrong handled_by edge feeds impact_analysis and find_path, so it is worse than the
// bug it fixes. The http_handler prop rejects the stub STRUCTURALLY — its signature is
// (ctx, string), not (http.ResponseWriter, *http.Request) — rather than by guessing.
func TestBindHTTPHandlers_RejectsNonHandlerBySignature(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		httpRoute("/api/weather/daily-range", "h.weatherHandler.GetDailyWeatherRange"),
		goHandler("internal/adapters/http/weather.HandlerV2.GetDailyWeatherRange"),
		// Same method name, service signature — must never win.
		goNonHandler("internal/bootstrap.NullWeatherService.GetDailyWeatherRange"),
		goNonHandler("internal/app/weather.Service.GetDailyWeatherRange"),
		goNonHandler("internal/mocks/services.MockWeatherService.GetDailyWeatherRange"),
	)

	eng.bindHTTPHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/api/weather/daily-range"})
	target, _ := routeHandledBy(routes[0])
	if want := "internal/adapters/http/weather.HandlerV2.GetDailyWeatherRange"; target != want {
		t.Errorf("bound to %q, want %q — the non-handler candidates must be rejected by signature", target, want)
	}
}

// TestBindHTTPHandlers_AmbiguousMethodNameSkipped mirrors grpcbind's ambiguity guard.
// Two genuine handlers sharing a method name cannot be told apart from the registration
// site alone, so the route is left unbound. Skipping is fail-safe; guessing is not.
// On fairwayhub/golf this covers 212 of 1639 routes.
func TestBindHTTPHandlers_AmbiguousMethodNameSkipped(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		httpRoute("/api/things", "h.thingHandler.List"),
		goHandler("internal/adapters/http/things.HandlerV2.List"),
		goHandler("internal/adapters/http/users.HandlerV2.List"),
	)

	eng.bindHTTPHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/api/things"})
	if target, ok := routeHandledBy(routes[0]); ok {
		t.Errorf("ambiguous method name bound anyway, to %q — two handlers share it", target)
	}
}

// TestBindHTTPHandlers_MiddlewareNotBound: a USE route's handler is middleware, not a
// (w, r) handler. It carries no http_handler prop, so it cannot bind — but assert it,
// because binding middleware to a route would pollute the same edges.
func TestBindHTTPHandlers_MiddlewareNotBound(t *testing.T) {
	eng, _ := New(config.Default())
	mw := httpRoute("/api", "h.auth.RequireAuth")
	mw.Props["method"] = "USE"
	mw.Props["type"] = "middleware"
	eng.Store().Add(mw, goHandler("internal/adapters/http/middleware.Auth.RequireAuth"))

	eng.bindHTTPHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/api"})
	if target, ok := routeHandledBy(routes[0]); ok {
		t.Errorf("middleware route was bound to %q", target)
	}
}

// TestBindHTTPHandlers_Idempotent: the pass reruns on every snapshot and append, so it
// must not accumulate duplicate edges.
func TestBindHTTPHandlers_Idempotent(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		httpRoute("/api/things", "h.thingHandler.List"),
		goHandler("internal/adapters/http/things.HandlerV2.List"),
	)

	eng.bindHTTPHandlers()
	eng.bindHTTPHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/api/things"})
	n := 0
	for _, r := range routes[0].Relations {
		if r.Kind == facts.RelHandledBy {
			n++
		}
	}
	if n != 1 {
		t.Errorf("handled_by edges = %d after two passes, want 1", n)
	}
}

// TestBindHTTPHandlers_CrossRepoNotBound: routes never bind across repos, exactly as
// grpcbind's per-repo index guarantees.
func TestBindHTTPHandlers_CrossRepoNotBound(t *testing.T) {
	eng, _ := New(config.Default())
	route := httpRoute("/api/things", "h.thingHandler.List")
	route.Repo = "api"
	handler := goHandler("internal/adapters/http/things.HandlerV2.List")
	handler.Repo = "other"
	eng.Store().Add(route, handler)

	eng.bindHTTPHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/api/things"})
	if target, ok := routeHandledBy(routes[0]); ok {
		t.Errorf("route bound to a handler in a different repo: %q", target)
	}
}
