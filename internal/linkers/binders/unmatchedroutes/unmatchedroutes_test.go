package unmatchedroutes

// The binder tags each client call site that resolves to no loaded server route with
// unmatched_by_server + a reason, so the aggregate coverage count becomes a queryable
// per-call list. This is the observability half of the coverage pass.

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/vocab"
)

func bind(t *testing.T, store *facts.Store) {
	t.Helper()
	if err := New(vocab.Default()).Bind(context.Background(), store); err != nil {
		t.Fatalf("Bind: %v", err)
	}
}

func clientRouteFact(repo, name, method string) facts.Fact {
	return facts.Fact{Kind: facts.KindRoute, Name: name, Repo: repo,
		Props: map[string]any{facts.PropRole: facts.RoleClient, "method": method}}
}

func serverRouteFact(repo, name, method string) facts.Fact {
	return facts.Fact{Kind: facts.KindRoute, Name: name, Repo: repo,
		Props: map[string]any{facts.PropRole: facts.RoleServer, "method": method}}
}

func TestFlagUnmatchedRoutes_ClientSide(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		clientRouteFact("app", "/api/items/{id}", "GET"),   // resolves to backend
		clientRouteFact("app", "/api/unknown/{id}", "GET"), // no server serves it -> path_unknown
		clientRouteFact("app", "/api/orders/{id}", "POST"), // path served, wrong verb -> method_mismatch
		clientRouteFact("app", "/health", "GET"),           // sub-2-segment path -> generic_path
		serverRouteFact("backend", "/api/items/{itemId}", "GET"),
		serverRouteFact("backend", "/api/orders/{orderId}", "GET"),
	)

	bind(t, store)

	props := map[string]map[string]any{}
	for _, f := range store.All() {
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
		if e, _ := got[PropUnmatchedByServer].(bool); !e {
			t.Errorf("%s should be unmatched_by_server; got %+v", tc.name, got)
		}
		if got[PropUnmatchedReason] != tc.wantReason {
			t.Errorf("%s reason = %v, want %s", tc.name, got[PropUnmatchedReason], tc.wantReason)
		}
	}

	items := props["/api/items/{id}"]
	if _, has := items[PropUnmatchedByServer]; has {
		t.Errorf("resolved client call must not be flagged; got %+v", items)
	}

	// The server route must never carry the client-side flag.
	server := props["/api/items/{itemId}"]
	if _, has := server[PropUnmatchedByServer]; has {
		t.Errorf("server route must not carry unmatched_by_server; got %+v", server)
	}
}

// TestFlagUnmatchedRoutes_ClearsStaleVerdict pins the idempotency property the binder
// depends on across appends: loading a second repo can RESOLVE a call site that was
// unmatched when only one repo was in the store, and the stale prop must not survive
// that. Without the clearing branches a re-link would leave every route carrying the
// verdict from whichever append first saw it.
func TestFlagUnmatchedRoutes_ClearsStaleVerdict(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		clientRouteFact("app", "/api/items/{id}", "GET"),
		// A second repo so the linker engages at all (it is a no-op single-repo).
		serverRouteFact("other", "/api/unrelated/{id}", "GET"),
	)

	bind(t, store)

	var flagged bool
	for _, f := range store.All() {
		if f.Name == "/api/items/{id}" {
			flagged = f.PropBool(PropUnmatchedByServer)
		}
	}
	if !flagged {
		t.Fatal("precondition: the call site should be unmatched with no server serving it")
	}

	// Now the backend appears, as it would on a second append.
	store.Add(serverRouteFact("backend", "/api/items/{itemId}", "GET"))
	bind(t, store)

	for _, f := range store.All() {
		if f.Name != "/api/items/{id}" {
			continue
		}
		if _, has := f.Props[PropUnmatchedByServer]; has {
			t.Errorf("stale unmatched_by_server survived a re-link that resolved the call; props=%+v", f.Props)
		}
		if _, has := f.Props[PropUnmatchedReason]; has {
			t.Errorf("stale unmatched_reason survived a re-link; props=%+v", f.Props)
		}
	}
}
