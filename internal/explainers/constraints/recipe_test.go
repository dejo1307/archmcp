package constraints

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

const recipeEventDriven = `
recipe: event-driven
roles:
  - name: events
  - name: bus
  - name: handlers
rules:
  - id: events-consumed
    require_edge: events
    to: handlers
    via: calls
    direction: inbound
    because: "An event nobody consumes is dead weight."
  - id: only-bus-calls-handlers
    protect: handlers
    owners: [bus]
    via: calls
    because: "Handlers are reached through the bus, never directly."
  - id: events-are-named
    require_name: events
    pattern: "*Event"
    because: "The suffix is the contract."
`

const recipeOrdersContext = `
use_recipe:
  - recipe: event-driven
    as: orders-events
    bind:
      events:   { match: ["app/events/orders/**"] }
      bus:      { match: ["app/lib/event_bus.rb"] }
      handlers: { match: ["app/handlers/orders/**"] }
    exempt:
      - rule: events-consumed
        witness: "LegacyOrderMigratedEvent has no inbound calls edge from orders-events/handlers"
        owner: "dana"
        because: "Fired only by the migration backfill, consumed manually."
        since: "2026-08-11"
`

const recipeBillingContext = `
use_recipe:
  - recipe: event-driven
    as: billing-events
    bind:
      events:   { match: ["app/events/billing/**"] }
      bus:      { match: ["app/lib/event_bus.rb"] }
      handlers: { match: ["app/handlers/billing/**"] }
`

func recipeWorld(t *testing.T) *facts.Store {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("enola/recipes/event-driven.yaml", recipeEventDriven)
	write("enola/constraints/orders.yaml", recipeOrdersContext)
	write("enola/constraints/billing.yaml", recipeBillingContext)
	d, err := intent.LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore()
	store.Add(intent.CompileFacts(d)...)
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "OrderPlacedEvent", File: "app/events/orders/order_placed_event.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "OrderCancelledEvent", File: "app/events/orders/order_cancelled_event.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "LegacyOrderMigratedEvent", File: "app/events/orders/legacy_order_migrated_event.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Orders::PlacedHandler", File: "app/handlers/orders/placed_handler.rb",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "OrderPlacedEvent"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "BillingInvoicedEvent", File: "app/events/billing/billing_invoiced_event.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::InvoicedHandler", File: "app/handlers/billing/invoiced_handler.rb",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "BillingInvoicedEvent"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "EventBus", File: "app/lib/event_bus.rb",
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "Orders::PlacedHandler"},
				{Kind: facts.RelCalls, Target: "Billing::InvoicedHandler"},
			}},
	)
	return store
}

func TestExplain_RecipeInstancesVerdictIndependentlyWithProvenance(t *testing.T) {
	insights, err := New().Explain(context.Background(), recipeWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	var violation, exempted *facts.Insight
	for i := range insights {
		in := insights[i]
		if strings.Contains(in.Title, "billing-events") {
			t.Errorf("the billing context is clean and must stay silent: %q", in.Title)
		}
		switch in.Title {
		case "Constraint orders-events/events-consumed violated: OrderCancelledEvent has no inbound calls edge from orders-events/handlers":
			violation = &insights[i]
		case "Exempted from constraint orders-events/events-consumed: LegacyOrderMigratedEvent has no inbound calls edge from orders-events/handlers":
			exempted = &insights[i]
		}
	}
	if violation == nil {
		t.Fatalf("insights = %+v, want the orphaned event verdicted under its instance-prefixed rule id", insights)
	}
	provenance := "This verdict traces to rule orders-events/events-consumed (recipe event-driven, instantiated in enola/constraints/orders.yaml)."
	if !strings.Contains(violation.Description, provenance) {
		t.Errorf("description = %q, want the recipe provenance %q", violation.Description, provenance)
	}
	if violation.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: an expanded rule is an ordinary rule", violation.Confidence)
	}
	if exempted == nil {
		t.Fatalf("insights = %+v, want the instance-scoped exemption reported as exempted", insights)
	}
	if !strings.Contains(exempted.Description, provenance) {
		t.Errorf("exempted description = %q, want the recipe provenance", exempted.Description)
	}
	if len(insights) != 2 {
		t.Fatalf("insights = %d, want exactly the violation and the exemption: %+v", len(insights), insights)
	}
}

func TestExplain_RecipeVerdictsAreDeterministic(t *testing.T) {
	first, err := New().Explain(context.Background(), recipeWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := New().Explain(context.Background(), recipeWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("runs differ in size: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Title != second[i].Title || first[i].Description != second[i].Description {
			t.Fatalf("runs differ at %d:\nfirst:  %+v\nsecond: %+v", i, first[i], second[i])
		}
	}
}
