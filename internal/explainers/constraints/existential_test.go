package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

func requireEdgeRuleIntent(id, requireEdge, to, via, direction, because string, exempt ...intent.ConstraintExemption) facts.Fact {
	props := map[string]any{"intent_kind": "rule", "rule": id, "require_edge": requireEdge,
		"via": via, "direction": direction, "because": because, "source": "wiki/p.md"}
	if to != "" {
		props["to"] = to
	}
	if encoded := intent.EncodeExemptions(exempt); encoded != "" {
		props["exempt"] = encoded
	}
	return facts.Fact{Kind: facts.KindIntent, Name: "rule: " + id, File: "wiki/p.md", Props: props}
}

const eventBecause = "An event nobody consumes is dead weight or a silent contract break."

func eventWorld(exempt ...intent.ConstraintExemption) *facts.Store {
	store := facts.NewStore()
	store.Add(
		componentIntent("events", "app/events/**"),
		componentIntent("handlers", "app/handlers/**"),
		requireEdgeRuleIntent("every-event-consumed", "events", "handlers", "calls", "inbound", eventBecause, exempt...),
		facts.Fact{Kind: facts.KindSymbol, Name: "OrderPlaced", File: "app/events/order_placed.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "OrderCancelled", File: "app/events/order_cancelled.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Handlers::OrderPlacedHandler", File: "app/handlers/order_placed_handler.rb",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "OrderPlaced"}}},
	)
	return store
}

func TestExplain_RequireEdgeOrphanedEventVerdictsAndConsumedOneStaysSilent(t *testing.T) {
	insights, err := New().Explain(context.Background(), eventWorld())
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	want := "Constraint every-event-consumed violated: OrderCancelled has no inbound calls edge from handlers"
	if got.Title != want {
		t.Errorf("title = %q, want %q", got.Title, want)
	}
	if got.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: the rule is declared and the absence is measured", got.Confidence)
	}
	if !strings.Contains(got.Description, "Because: "+eventBecause) {
		t.Errorf("description must surface the rule's rationale, got: %q", got.Description)
	}
	if strings.Contains(got.Title, "OrderPlaced ") {
		t.Errorf("title = %q: the consumed event satisfies the rule and must stay silent", got.Title)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].File != "app/events/order_cancelled.rb" ||
		got.Evidence[0].Symbol != "OrderCancelled" {
		t.Errorf("evidence = %+v, want the orphaned event and its file", got.Evidence)
	}
}

func TestExplain_RequireEdgeExemptedOrphanLandsInTheExemptedBucket(t *testing.T) {
	insights, err := New().Explain(context.Background(), eventWorld(intent.ConstraintExemption{
		Witness: "OrderCancelled has no inbound calls edge from handlers",
		Owner:   "alice",
		Because: "the cancellation consumer ships with the refunds service in Q4",
		Since:   "2026-08-10",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	want := "Exempted from constraint every-event-consumed: OrderCancelled has no inbound calls edge from handlers"
	if got.Title != want {
		t.Errorf("title = %q, want %q", got.Title, want)
	}
	if got.Confidence != exemptedConfidence {
		t.Errorf("confidence = %v, want %v — counted and visible, never gating", got.Confidence, exemptedConfidence)
	}
	for _, in := range insights {
		if strings.Contains(in.Title, "violated") {
			t.Errorf("an exempted witness must not also verdict: %q", in.Title)
		}
	}
}

func TestExplain_RequireEdgeUnmeasurableSourcesSkipWithANamedCount(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("events", "app/events/**"),
		componentIntent("handlers", "config/handlers/**"),
		requireEdgeRuleIntent("every-event-consumed", "events", "handlers", "calls", "inbound", eventBecause),
		facts.Fact{Kind: facts.KindSymbol, Name: "OrderPlaced", File: "app/events/order_placed.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "OrderCancelled", File: "app/events/order_cancelled.rb"},
		facts.Fact{Kind: facts.KindModule, Name: "config/handlers/orders", File: "config/handlers/orders.yaml",
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: "OrderPlaced"}}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var skip *facts.Insight
	for i := range insights {
		if strings.Contains(insights[i].Title, "violated") {
			t.Fatalf("a member whose edge visibility is unmeasurable must never verdict: %q", insights[i].Title)
		}
		if strings.HasPrefix(insights[i].Title, "require_edge rule every-event-consumed skipped:") {
			skip = &insights[i]
		}
	}
	if skip == nil {
		t.Fatalf("insights = %+v, want the named skip advisory", insights)
	}
	if !strings.Contains(skip.Title, "2 member(s)") {
		t.Errorf("title = %q, want the skip count named", skip.Title)
	}
	if skip.Confidence != edgeSkipConfidence {
		t.Errorf("confidence = %v, want %v — no verdict was reached, and silence must stay visible", skip.Confidence, edgeSkipConfidence)
	}
	for _, part := range []string{"OrderCancelled", "OrderPlaced", ".yaml files"} {
		if !strings.Contains(skip.Description, part) {
			t.Errorf("description must name %q, got: %q", part, skip.Description)
		}
	}
}

func TestExplain_RequireEdgeOutboundVerdictsTheEdgelessMember(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("producers", "app/producers/**"),
		componentIntent("bus", "app/bus/**"),
		requireEdgeRuleIntent("producers-publish", "producers", "bus", "calls", "outbound",
			"a producer that never publishes is dead weight"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Producers::Order", File: "app/producers/order.rb",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Bus::Publish"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Producers::Silent", File: "app/producers/silent.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Bus::Publish", File: "app/bus/publish.rb"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	want := "Constraint producers-publish violated: Producers::Silent has no outbound calls edge into bus"
	if insights[0].Title != want {
		t.Errorf("title = %q, want %q", insights[0].Title, want)
	}
}

func TestExplain_RequireEdgeOutboundUnmeasurableMemberIsCountedAsSkipped(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: tables", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "component", "component": "tables",
				"match": "db/**", "kind": "storage", "source": "wiki/p.md"}},
		requireEdgeRuleIntent("tables-feed-something", "tables", "", "calls", "outbound",
			"a table nothing reads is a migration debt"),
		facts.Fact{Kind: facts.KindStorage, Name: "invoices", File: "db/structure.sql",
			Props: map[string]any{"columns": "id total"}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly the skip advisory: %+v", len(insights), insights)
	}
	got := insights[0]
	if !strings.HasPrefix(got.Title, "require_edge rule tables-feed-something skipped:") ||
		!strings.Contains(got.Title, "1 member(s)") {
		t.Errorf("title = %q, want the named skip with its count", got.Title)
	}
	if !strings.Contains(got.Description, "invoices") {
		t.Errorf("description must name the skipped member, got: %q", got.Description)
	}
}

func TestExplain_RequireEdgeSkipSuppressesDeadExemptionWarnings(t *testing.T) {
	store := eventWorld(intent.ConstraintExemption{
		Witness: "GhostEvent has no inbound calls edge from handlers",
		Owner:   "bob",
		Because: "ghost never existed",
		Since:   "2026-01-01",
	})
	store.Add(facts.Fact{Kind: facts.KindSymbol, Name: "Handlers::Config", File: "app/handlers/wiring.yaml",
		Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: "OrderPlaced"}}})
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range insights {
		if strings.HasPrefix(in.Title, "Constraint exemption on ") {
			t.Errorf("a skipped rule must not warn about dead exemptions — the witness may hide in the skipped set: %q", in.Title)
		}
		if strings.Contains(in.Title, "violated") {
			t.Errorf("a partially blind source scope must not verdict: %q", in.Title)
		}
	}
}

func TestContractFor_RequireEdgeStatesTheObligation(t *testing.T) {
	bindings, declared := ContractFor(eventWorld(), "app/events/order_refunded.rb")
	if !declared {
		t.Fatal("components are declared; the contract must answer")
	}
	if len(bindings) != 1 || bindings[0].Component != "events" {
		t.Fatalf("bindings = %+v, want the events component", bindings)
	}
	if len(bindings[0].Rules) != 1 {
		t.Fatalf("rules = %+v, want the existential rule bound", bindings[0].Rules)
	}
	got := bindings[0].Rules[0]
	want := "members of events must have an inbound calls edge from handlers"
	if got.Statement != want {
		t.Errorf("statement = %q, want %q", got.Statement, want)
	}
	if got.Because != eventBecause {
		t.Errorf("because = %q, want the declared rationale", got.Because)
	}
}
