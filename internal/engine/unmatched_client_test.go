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
		clientRouteFact("app", "/api/items/{id}", "GET"),   // resolves to backend
		clientRouteFact("app", "/api/unknown/{id}", "GET"), // no server -> no_match
		facts.Fact{Kind: facts.KindRoute, Name: "/api/items/{itemId}", Repo: "backend",
			Props: map[string]any{"role": "server", "method": "GET"}},
	)

	eng.flagUnmatchedRoutes()

	props := map[string]map[string]any{}
	for _, f := range eng.store.All() {
		if f.Kind == facts.KindRoute {
			props[f.Name] = f.Props
		}
	}

	unknown := props["/api/unknown/{id}"]
	if e, _ := unknown["unmatched_by_server"].(bool); !e {
		t.Errorf("/api/unknown should be unmatched_by_server; got %+v", unknown)
	}
	if unknown["unmatched_reason"] != "path_unknown" {
		t.Errorf("/api/unknown reason = %v, want path_unknown", unknown["unmatched_reason"])
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
