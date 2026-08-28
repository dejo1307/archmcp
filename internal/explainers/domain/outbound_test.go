package domain

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The outbound-integrations finding used to cite its endpoints by DISPLAY LABEL
// — the HTTP method glued onto the route path — in Evidence.Fact, a field that
// means "a fact's name". No fact carries such a name, so every one of those
// citations pointed at nothing: 46 entries on one measured repository, silently,
// because nothing checked that a cited name exists.
func TestOutboundIntegrations_CitesFactsThatExist(t *testing.T) {
	store := facts.NewStore()
	// Two client calls from one component, which is minOutboundCalls.
	store.Add(
		facts.Fact{
			Kind: facts.KindRoute, Name: "/v1/datasets/{}", File: "app/client.py", Line: 12,
			Props: map[string]any{"role": "client", "method": "DELETE"},
		},
		facts.Fact{
			Kind: facts.KindRoute, Name: "/v1/datasets", File: "app/client.py", Line: 20,
			Props: map[string]any{"role": "client", "method": "GET"},
		},
	)

	insights := outboundIntegrations(store)
	if len(insights) == 0 {
		t.Fatal("no outbound finding: the fixture no longer exercises this")
	}

	names := map[string]bool{}
	for _, f := range store.FactsRef() {
		names[f.Name] = true
	}

	cited := 0
	for _, in := range insights {
		for _, e := range in.Evidence {
			if e.Fact == "" {
				continue
			}
			cited++
			if !names[e.Fact] {
				t.Errorf("evidence cites %q, which names no fact in the snapshot", e.Fact)
			}
			if e.File == "" {
				t.Errorf("evidence for %q carries no file, so a reader cannot reach the call site", e.Fact)
			}
		}
	}
	if cited == 0 {
		t.Error("the finding cited no fact at all")
	}
}

// The method used to live in the cited name. It has to survive somewhere, or the
// finding stops telling a reader which verb the call used.
func TestOutboundIntegrations_KeepsTheMethodInTheDetail(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{
			Kind: facts.KindRoute, Name: "/v1/datasets/{}", File: "app/client.py",
			Props: map[string]any{"role": "client", "method": "DELETE"},
		},
		facts.Fact{
			Kind: facts.KindRoute, Name: "/v1/datasets", File: "app/client.py",
			Props: map[string]any{"role": "client", "method": "GET"},
		},
	)

	var details []string
	for _, in := range outboundIntegrations(store) {
		for _, e := range in.Evidence {
			details = append(details, e.Detail)
		}
	}
	want := map[string]bool{"DELETE called from here": true, "GET called from here": true}
	for _, d := range details {
		delete(want, d)
	}
	if len(want) > 0 {
		t.Errorf("these details never appeared: %v (got %v)", want, details)
	}
}

// A route this repository also SERVES is not an outbound integration, and the
// fix must not have disturbed that check.
func TestOutboundIntegrations_SkipsRoutesServedHere(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindRoute, Name: "/local", File: "app/client.py",
			Props: map[string]any{"role": "client", "method": "GET"}},
		facts.Fact{Kind: facts.KindRoute, Name: "/local", File: "app/server.py",
			Props: map[string]any{"role": "server", "method": "GET"}},
		facts.Fact{Kind: facts.KindRoute, Name: "/remote", File: "app/client.py",
			Props: map[string]any{"role": "client", "method": "GET"}},
	)
	for _, in := range outboundIntegrations(store) {
		for _, e := range in.Evidence {
			if e.Fact == "/local" {
				t.Error("a route served by this repository was reported as an outbound integration")
			}
		}
	}
}
