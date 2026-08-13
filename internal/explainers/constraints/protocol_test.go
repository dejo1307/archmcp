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

func protocolRuleIntent(id, protocol string, steps []string, via, because string, exempt ...intent.ConstraintExemption) facts.Fact {
	props := map[string]any{"intent_kind": "rule", "rule": id, "protocol": protocol,
		"steps": strings.Join(steps, " "), "via": via, "verification": "structural",
		"because": because, "source": "wiki/p.md"}
	if encoded := intent.EncodeExemptions(exempt); encoded != "" {
		props["exempt"] = encoded
	}
	return facts.Fact{Kind: facts.KindIntent, Name: "rule: " + id, File: "wiki/p.md", Props: props}
}

const checkoutBecause = "Charging without reserving oversells; reserving without validating reserves garbage."

var checkoutSteps = []string{"validate-cart", "reserve-stock", "charge-payment"}

func checkoutWorld(exempt ...intent.ConstraintExemption) *facts.Store {
	store := facts.NewStore()
	store.Add(
		componentIntent("checkout-callers", "app/checkout/**"),
		componentIntent("validate-cart", "app/steps/validate/**"),
		componentIntent("reserve-stock", "app/steps/reserve/**"),
		componentIntent("charge-payment", "app/steps/charge/**"),
		protocolRuleIntent("checkout-protocol", "checkout-callers", checkoutSteps, "calls", checkoutBecause, exempt...),
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ValidateCart", File: "app/steps/validate/validate_cart.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ReserveStock", File: "app/steps/reserve/reserve_stock.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ChargePayment", File: "app/steps/charge/charge_payment.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::CompleteFlow", File: "app/checkout/complete_flow.rb",
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "Steps::ValidateCart"},
				{Kind: facts.RelCalls, Target: "Steps::ReserveStock"},
				{Kind: facts.RelCalls, Target: "Steps::ChargePayment"},
			}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::OrderFlow", File: "app/checkout/order_flow.rb",
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "Steps::ValidateCart"},
				{Kind: facts.RelCalls, Target: "Steps::ChargePayment"},
			}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::CartPreview", File: "app/checkout/cart_preview.rb",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Steps::ValidateCart"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::Bystander", File: "app/checkout/bystander.rb"},
		facts.Fact{Kind: facts.KindModule, Name: "checkout/wiring", File: "app/checkout/wiring.yaml",
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: "Steps::ChargePayment"}}},
	)
	return store
}

func TestExplain_ProtocolStepSkipperVerdictsAndConformantCallersStaySilent(t *testing.T) {
	insights, err := New().Explain(context.Background(), checkoutWorld())
	if err != nil {
		t.Fatal(err)
	}
	var violation, skip *facts.Insight
	for i := range insights {
		for _, silent := range []string{"CompleteFlow", "CartPreview", "Bystander"} {
			if strings.Contains(insights[i].Title, silent) {
				t.Errorf("%s conforms or participates in no step and must stay silent: %q", silent, insights[i].Title)
			}
		}
		switch {
		case strings.Contains(insights[i].Title, "violated"):
			violation = &insights[i]
		case strings.HasPrefix(insights[i].Title, "protocol rule checkout-protocol skipped:"):
			skip = &insights[i]
		}
	}
	if len(insights) != 2 {
		t.Fatalf("insights = %d, want exactly the violation and the skip advisory: %+v", len(insights), insights)
	}
	if violation == nil {
		t.Fatal("the step-skipping caller must verdict")
	}
	want := "Constraint checkout-protocol violated: Checkout::OrderFlow calls charge-payment without reserve-stock"
	if violation.Title != want {
		t.Errorf("title = %q, want %q", violation.Title, want)
	}
	if violation.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: the rule is declared and the structural skip is measured", violation.Confidence)
	}
	for _, part := range []string{
		"structural protocol conformance, not runtime ordering",
		"whether the steps execute in order at runtime it cannot see and does not claim",
		"validate-cart -> reserve-stock -> charge-payment",
		"Because: " + checkoutBecause,
	} {
		if !strings.Contains(violation.Description, part) {
			t.Errorf("description must carry %q, got: %q", part, violation.Description)
		}
	}
	if len(violation.Evidence) != 1 || violation.Evidence[0].File != "app/checkout/order_flow.rb" ||
		violation.Evidence[0].Symbol != "Checkout::OrderFlow" || violation.Evidence[0].Fact != "reserve-stock" {
		t.Errorf("evidence = %+v, want the skipping caller and the skipped step", violation.Evidence)
	}
	if skip == nil {
		t.Fatal("the unmeasurable member must land in the named skip advisory")
	}
	if !strings.Contains(skip.Title, "1 member(s)") || !strings.Contains(skip.Description, "checkout/wiring") {
		t.Errorf("skip advisory must count and name the unmeasurable member, got title %q description %q", skip.Title, skip.Description)
	}
	if skip.Confidence != protocolSkipConfidence {
		t.Errorf("skip confidence = %v, want %v — no verdict was reached, and silence must stay visible", skip.Confidence, protocolSkipConfidence)
	}
}

func TestExplain_ProtocolNamesTheHighestSkippedStepAndListsEveryMissingOne(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("checkout-callers", "app/checkout/**"),
		componentIntent("validate-cart", "app/steps/validate/**"),
		componentIntent("reserve-stock", "app/steps/reserve/**"),
		componentIntent("charge-payment", "app/steps/charge/**"),
		protocolRuleIntent("checkout-protocol", "checkout-callers", checkoutSteps, "calls", checkoutBecause),
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ChargePayment", File: "app/steps/charge/charge_payment.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::ChargeOnly", File: "app/checkout/charge_only.rb",
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Steps::ChargePayment"}}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var violation *facts.Insight
	for i := range insights {
		if strings.Contains(insights[i].Title, "violated") {
			violation = &insights[i]
		}
	}
	if violation == nil {
		t.Fatalf("insights = %+v, want the two-step skip verdicted", insights)
	}
	want := "Constraint checkout-protocol violated: Checkout::ChargeOnly calls charge-payment without reserve-stock"
	if violation.Title != want {
		t.Errorf("title = %q, want the highest skipped step named: %q", violation.Title, want)
	}
	if !strings.Contains(violation.Description, "validate-cart, reserve-stock") {
		t.Errorf("description must list every missing prerequisite in step order, got: %q", violation.Description)
	}
}

func TestExplain_ProtocolExemptedSkipperLandsInTheExemptedBucket(t *testing.T) {
	insights, err := New().Explain(context.Background(), checkoutWorld(intent.ConstraintExemption{
		Witness: "Checkout::OrderFlow calls charge-payment without reserve-stock",
		Owner:   "alice",
		Because: "the legacy flow reserves through the warehouse service until Q4",
		Since:   "2026-08-11",
	}))
	if err != nil {
		t.Fatal(err)
	}
	exempted := false
	for _, in := range insights {
		if strings.Contains(in.Title, "violated") {
			t.Errorf("an exempted witness must not also verdict: %q", in.Title)
		}
		if in.Title == "Exempted from constraint checkout-protocol: Checkout::OrderFlow calls charge-payment without reserve-stock" {
			exempted = true
			if in.Confidence != exemptedConfidence {
				t.Errorf("confidence = %v, want %v — counted and visible, never gating", in.Confidence, exemptedConfidence)
			}
		}
	}
	if !exempted {
		t.Fatalf("insights = %+v, want the member identity carved into the Exempted bucket", insights)
	}
}

func TestExplain_ProtocolDeadStepSelectorRaisesTheAdvisory(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("checkout-callers", "app/checkout/**"),
		componentIntent("validate-cart", "app/steps/validate/**"),
		componentIntent("reserve-stock", "app/steps/reserve/**"),
		componentIntent("charge-payment", "app/steps/charge/**"),
		protocolRuleIntent("checkout-protocol", "checkout-callers", checkoutSteps, "calls", checkoutBecause),
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ValidateCart", File: "app/steps/validate/validate_cart.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ChargePayment", File: "app/steps/charge/charge_payment.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::CompleteFlow", File: "app/checkout/complete_flow.rb",
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "Steps::ValidateCart"},
				{Kind: facts.RelCalls, Target: "Steps::ChargePayment"},
			}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	advisory := false
	for _, in := range insights {
		if in.Title == "Constraint component reserve-stock matches nothing" {
			advisory = true
		}
	}
	if !advisory {
		t.Fatalf("insights = %+v, want the dead-selector advisory for the empty step", insights)
	}
}

const checkoutRecipeYAML = `
recipe: checkout
roles:
  - name: callers
  - name: validate
  - name: reserve
  - name: charge
rules:
  - id: order
    protocol: callers
    steps: [validate, reserve, charge]
    via: calls
    because: "Charging without reserving oversells; reserving without validating reserves garbage."
`

const checkoutInstantiationYAML = `
use_recipe:
  - recipe: checkout
    as: web-checkout
    bind:
      callers:  { match: ["app/checkout/**"] }
      validate: { match: ["app/steps/validate/**"] }
      reserve:  { match: ["app/steps/reserve/**"] }
      charge:   { match: ["app/steps/charge/**"] }
`

func checkoutRecipeWorld(t *testing.T) *facts.Store {
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
	write("enola/recipes/checkout.yaml", checkoutRecipeYAML)
	write("enola/constraints/checkout.yaml", checkoutInstantiationYAML)
	d, err := intent.LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore()
	store.Add(intent.CompileFacts(d)...)
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ValidateCart", File: "app/steps/validate/validate_cart.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ReserveStock", File: "app/steps/reserve/reserve_stock.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ChargePayment", File: "app/steps/charge/charge_payment.rb"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::CompleteFlow", File: "app/checkout/complete_flow.rb",
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "Steps::ValidateCart"},
				{Kind: facts.RelCalls, Target: "Steps::ReserveStock"},
				{Kind: facts.RelCalls, Target: "Steps::ChargePayment"},
			}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::OrderFlow", File: "app/checkout/order_flow.rb",
			Relations: []facts.Relation{
				{Kind: facts.RelCalls, Target: "Steps::ValidateCart"},
				{Kind: facts.RelCalls, Target: "Steps::ChargePayment"},
			}},
	)
	return store
}

func TestExplain_ProtocolInsideARecipeVerdictsOverRoleBoundSteps(t *testing.T) {
	insights, err := New().Explain(context.Background(), checkoutRecipeWorld(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly the step-skip verdict: %+v", len(insights), insights)
	}
	got := insights[0]
	want := "Constraint web-checkout/order violated: Checkout::OrderFlow calls web-checkout/charge without web-checkout/reserve"
	if got.Title != want {
		t.Errorf("title = %q, want %q", got.Title, want)
	}
	provenance := "This verdict traces to rule web-checkout/order (recipe checkout, instantiated in enola/constraints/checkout.yaml)."
	if !strings.Contains(got.Description, provenance) {
		t.Errorf("description = %q, want the recipe provenance %q", got.Description, provenance)
	}
	if !strings.Contains(got.Description, "structural protocol conformance, not runtime ordering") {
		t.Errorf("an expanded protocol rule keeps the verification honesty line, got: %q", got.Description)
	}
}

func TestContractFor_ProtocolStatesTheOrderedObligation(t *testing.T) {
	bindings, declared := ContractFor(checkoutWorld(), "app/checkout/new_flow.rb")
	if !declared {
		t.Fatal("components are declared; the contract must answer")
	}
	if len(bindings) != 1 || bindings[0].Component != "checkout-callers" {
		t.Fatalf("bindings = %+v, want the checkout-callers component", bindings)
	}
	if len(bindings[0].Rules) != 1 {
		t.Fatalf("rules = %+v, want the protocol rule bound", bindings[0].Rules)
	}
	got := bindings[0].Rules[0]
	want := "members of checkout-callers that reach charge-payment via calls must also reach reserve-stock, validate-cart, in the declared order of obligation — structural conformance, not runtime ordering"
	if got.Statement != want {
		t.Errorf("statement = %q, want %q", got.Statement, want)
	}
	if got.Because != checkoutBecause {
		t.Errorf("because = %q, want the declared rationale", got.Because)
	}
}
