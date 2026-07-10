package engine

// flagUnmatchedRoutes tags each client call site that resolves to no loaded server
// route with unmatched_by_server + a reason, so the aggregate coverage count becomes
// a queryable per-call list. This is the observability half of the coverage pass.

import (
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/facts"
)

func clientRouteFact(repo, name, method string) facts.Fact {
	return facts.Fact{Kind: facts.KindRoute, Name: name, Repo: repo,
		Props: map[string]any{"role": "client", "method": method}}
}

func TestFlagUnmatchedRoutes_ClientSide(t *testing.T) {
	eng, err := New(config.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	eng.store.Add(
		clientRouteFact("app", "/api/items/{id}", "GET"),    // resolves to backend
		clientRouteFact("app", "/api/unknown/{id}", "GET"),  // no server serves it -> path_unknown
		clientRouteFact("app", "/api/orders/{id}", "POST"),  // path served, wrong verb -> method_mismatch
		clientRouteFact("app", "/health", "GET"),            // sub-2-segment path -> generic_path
		facts.Fact{Kind: facts.KindRoute, Name: "/api/items/{itemId}", Repo: "backend",
			Props: map[string]any{"role": "server", "method": "GET"}},
		facts.Fact{Kind: facts.KindRoute, Name: "/api/orders/{orderId}", Repo: "backend",
			Props: map[string]any{"role": "server", "method": "GET"}},
	)

	eng.flagUnmatchedRoutes()

	props := map[string]map[string]any{}
	for _, f := range eng.store.All() {
		if f.Kind == facts.KindRoute {
			props[f.Name] = f.Props
		}
	}

	// Each reason the resolver can emit for an unresolved client call, asserted by name
	// so the value set stays pinned to the crossrepo.Reason* constants.
	for _, tc := range []struct {
		name, wantReason string
	}{
		{"/api/unknown/{id}", "path_unknown"},
		{"/api/orders/{id}", "method_mismatch"},
		{"/health", "generic_path"},
	} {
		got := props[tc.name]
		if e, _ := got["unmatched_by_server"].(bool); !e {
			t.Errorf("%s should be unmatched_by_server; got %+v", tc.name, got)
		}
		if got["unmatched_reason"] != tc.wantReason {
			t.Errorf("%s reason = %v, want %s", tc.name, got["unmatched_reason"], tc.wantReason)
		}
	}

	items := props["/api/items/{id}"]
	if _, has := items["unmatched_by_server"]; has {
		t.Errorf("resolved client call must not be flagged; got %+v", items)
	}

	// The server route must never carry the client-side flag.
	server := props["/api/items/{itemId}"]
	if _, has := server["unmatched_by_server"]; has {
		t.Errorf("server route must not carry unmatched_by_server; got %+v", server)
	}
}
