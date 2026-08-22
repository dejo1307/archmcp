package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func method(name, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Repo: "app", Props: map[string]any{"symbol_kind": "method", "language": "ruby", "exported": true}}
}

// with_* requires without_*: the member whose sibling exists is fine, the one
// without it is named with the sibling the convention asks for, and a member
// outside the pattern is never asked.
func TestRequireName_PairsThroughTheCapturedBase(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("chat", "app/chat/**"),
		facts.Fact{Kind: facts.KindIntent, Name: "rule: paired-scopes", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "paired-scopes", "require_name": "chat", "pattern": "with_*", "requires": "without_*", "because": "every with_ has a without_", "source": "wiki/p.md"}},
		method("Room#with_history", "app/chat/room.rb"),
		method("Room#without_history", "app/chat/room.rb"),
		method("Room#with_guests", "app/chat/room.rb"),
		method("Room#archive", "app/chat/room.rb"),
	)
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, i := range got {
		if strings.Contains(i.Title, "paired-scopes") {
			titles = append(titles, i.Title)
		}
	}
	if len(titles) != 1 || !strings.Contains(titles[0], "Room#with_guests has no Room#without_guests") {
		t.Fatalf("want the one unpaired member named with its sibling, got %v", titles)
	}
}

// A component's public files make their members visible to private, beside
// the measured exported prop: reaching a public file from outside is fine,
// reaching an internal one is the breach.
func TestPrivate_PublicPathsAreVisible(t *testing.T) {
	store := facts.NewStore()
	internalMethod := func(name, file string) facts.Fact {
		f := method(name, file)
		f.Props["exported"] = false
		return f
	}
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: billing", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "component", "component": "billing", "match": "app/billing/**", "public": "app/billing/public/**", "source": "wiki/p.md"}},
		componentIntent("orders", "app/orders/**"),
		facts.Fact{Kind: facts.KindIntent, Name: "rule: billing-surface", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "billing-surface", "private": "billing", "because": "billing is reached through its public files", "source": "wiki/p.md"}},
		internalMethod("Billing::Public::Charge#call", "app/billing/public/charge.rb"),
		internalMethod("Billing::Ledger#post", "app/billing/ledger.rb"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Orders::Checkout#run", File: "app/orders/checkout.rb", Repo: "app",
			Props:     map[string]any{"symbol_kind": "method", "language": "ruby", "exported": true},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Billing::Public::Charge#call"}, {Kind: facts.RelCalls, Target: "Billing::Ledger#post"}}},
	)
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, i := range got {
		if strings.Contains(i.Title, "billing-surface") {
			titles = append(titles, i.Title)
		}
	}
	if len(titles) != 1 || !strings.Contains(titles[0], "Billing::Ledger#post") || strings.Contains(titles[0], "Charge") {
		t.Fatalf("the public file is reachable and the internal one is not, got %v", titles)
	}
}

// receiver: none narrows a literal to receiver-less calls.
func TestForbidToName_ReceiverNoneMatchesOnlyBareCalls(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("models", "app/models/**"),
		facts.Fact{Kind: facts.KindIntent, Name: "rule: bare-params", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "bare-params", "forbid": "models", "to_name": "params", "receiver": "none", "via": "calls", "because": "the request's params without a receiver is the controller's", "source": "wiki/p.md"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Order#total", File: "app/models/order.rb", Repo: "app",
			Props:     map[string]any{"symbol_kind": "method", "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "params"}, {Kind: facts.RelCalls, Target: "request.params"}}},
	)
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, i := range got {
		if strings.Contains(i.Title, "bare-params violated") {
			titles = append(titles, i.Title)
		}
	}
	if len(titles) != 1 || !strings.Contains(titles[0], "-> params via") {
		t.Fatalf("only the receiver-less call matches, got %v", titles)
	}
}

// explain reads the same membership the evaluator verdicts on: the
// components whose selector admits a fact in the file, the selector stated,
// and the edges the file makes.
func TestExplainFile_NamesComponentsAndEdges(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("models", "app/models/**"),
		predicateComponentIntent("records", nil, map[string]any{"ancestor": "ApplicationRecord"}),
		rubyClass("Order", "app/models/order.rb", "ApplicationRecord"),
		resolvedAncestor("Order", "app/models/order.rb", "ApplicationRecord", 1),
		facts.Fact{Kind: facts.KindSymbol, Name: "Order#total", File: "app/models/order.rb", Repo: "app", Line: 4,
			Props:     map[string]any{"symbol_kind": "method", "language": "ruby"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Tax.for"}, {Kind: facts.RelDeclares, Target: "app/models"}}},
	)
	e := ExplainFile(store, "app/models/order.rb")
	if len(e.Memberships) != 2 || e.Memberships[0].Component != "models" || e.Memberships[1].Component != "records" {
		t.Fatalf("memberships = %+v", e.Memberships)
	}
	if e.Memberships[0].Selector != "match app/models/**" || !strings.Contains(e.Memberships[1].Selector, "ancestor ApplicationRecord") {
		t.Fatalf("selectors = %+v", e.Memberships)
	}
	var calls, declares int
	for _, edge := range e.Outgoing {
		switch edge.Kind {
		case facts.RelCalls:
			calls++
			if edge.Target != "Tax.for" || edge.Line != 4 {
				t.Fatalf("the call edge must carry its target and line: %+v", edge)
			}
		case facts.RelDeclares:
			declares++
		}
	}
	if calls != 1 || declares != 0 {
		t.Fatalf("outgoing = %+v (declares edges are not outgoing)", e.Outgoing)
	}
}
