package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The basis vocabulary, exercised at both ends of an edge and in every form
// that words one. These tests moved here from grounding_test.go with the
// helpers they assert on: three boolean helpers became one three-state
// vocabulary, so the sentences they graded are no longer grounding's to state.
//
// Two shipped sentences changed deliberately and are pinned here in their new
// form. The two grounded wordings — "the measured file its edge names" and "the
// measured file it names" — were one statement said twice, and one vocabulary
// says it once. And a verdict whose SOURCE was a dependency carrier used to
// report "both memberships are exact": the carrier is a file-level join, never
// a member, and the sentence now says so.

// The grounded verdict must say it grounded. Calling a file-joined membership
// exact would claim a precision the join did not have.
func TestExplain_GroundedVerdictDoesNotClaimAnExactMembership(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("views", "app/views/**"),
		componentIntent("js-controllers", "app/javascript/controllers/**"),
		ruleIntent("views-avoid-controllers", "views", "js-controllers", "depends_on", "a view must not bind a controller directly"),
		markupBinding("app/views/jobs/show.html.erb", "app/javascript/controllers/dropdown_controller.js"),
		facts.Fact{Kind: facts.KindFileRef, Name: "app/javascript/controllers/dropdown_controller.js", File: "app/javascript/controllers/dropdown_controller.js"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if !strings.HasPrefix(insight.Title, "Constraint views-avoid-controllers violated") {
			continue
		}
		if strings.Contains(insight.Description, "both memberships are exact") {
			t.Errorf("a grounded membership was reported as exact: %q", insight.Description)
		}
		if !strings.Contains(insight.Description, "grounds on the measured file it names") {
			t.Errorf("the verdict must state that its target grounded, got: %q", insight.Description)
		}
		return
	}
	t.Fatalf("no verdict to inspect: %+v", insights)
}

// Grounding is strictly subordinate: a target that names a member fact exactly
// is answered by the exact name, and the verdict says so. The fallback is only
// ever reached by a target the exact-name lookup already failed.
func TestExplain_ExactNameMembershipWinsOverGrounding(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("views", "app/views/**"),
		componentIntent("js-controllers", "app/javascript/controllers/**"),
		ruleIntent("views-avoid-controllers", "views", "js-controllers", "depends_on", "a view must not bind a controller directly"),
		facts.Fact{Kind: facts.KindDependency, Name: "stimulus-binding: app/views/jobs/show.html.erb -> dropdown", File: "app/views/jobs/show.html.erb",
			Props:     map[string]any{"framework": "stimulus", "resolution_level": "markup-declared"},
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: "app/javascript/controllers.DropdownController"}}},
		facts.Fact{Kind: facts.KindSymbol, Name: "app/javascript/controllers.DropdownController", File: "app/javascript/controllers/dropdown_controller.js"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if !strings.HasPrefix(insight.Title, "Constraint views-avoid-controllers violated") {
			continue
		}
		if !strings.Contains(insight.Description, "the target membership is exact") {
			t.Errorf("the target names a member fact exactly, so the verdict must report an exact membership, got: %q", insight.Description)
		}
		if !strings.Contains(insight.Description, "rides a dependency fact of a file the component's patterns name") {
			t.Errorf("the source is a dependency carrier, not a member, and the sentence must say which: %q", insight.Description)
		}
		return
	}
	t.Fatalf("a target naming a member exactly must still verdict: %+v", insights)
}

// allow_only claimed "the target names a measured fact" for a target that names
// a measured FILE. The claim is reachable through imports and this branch opens
// it to every Stimulus binding, so the verdict states which resolution it had.
func TestExplain_AllowOnlyGroundedTargetDoesNotClaimAMeasuredFact(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("views", "app/views/**"),
		componentIntent("js-controllers", "app/javascript/controllers/**"),
		ruleIntentProps("views-reach-controllers-only", map[string]any{
			"allow": "views", "only": "js-controllers", "via": "depends_on",
			"because": "a view may bind only the controllers it declares"}),
		markupBinding("app/views/jobs/show.html.erb", "app/legacy/widgets/thing_controller.js"),
		facts.Fact{Kind: facts.KindFileRef, Name: "app/legacy/widgets/thing_controller.js", File: "app/legacy/widgets/thing_controller.js"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if !strings.HasPrefix(insight.Title, "Constraint views-reach-controllers-only violated") {
			continue
		}
		if strings.Contains(insight.Description, "the target names a measured fact") {
			t.Errorf("the target names a measured file, not a fact: %q", insight.Description)
		}
		if !strings.Contains(insight.Description, "grounds on the measured file it names") {
			t.Errorf("the verdict must state that its target grounded, got: %q", insight.Description)
		}
		return
	}
	t.Fatalf("the edge lands outside every allowed component and its target resolves, so the rule must verdict: %+v", insights)
}

// The exact form keeps its own sentence: a target that names a measured fact is
// a stronger resolution than a grounded one, and blurring the two is the defect
// in the other direction.
func TestExplain_AllowOnlyExactTargetStillNamesAMeasuredFact(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("domain", "app/domain/**"),
		componentIntent("contracts", "app/contracts/**"),
		ruleIntentProps("domain-reaches-contracts-only", map[string]any{
			"allow": "domain", "only": "contracts", "via": "depends_on",
			"because": "the domain speaks to the world through its contracts"}),
		facts.Fact{Kind: facts.KindModule, Name: "app/domain/billing", File: "app/domain/billing",
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: "app/adapters/http"}}},
		facts.Fact{Kind: facts.KindModule, Name: "app/adapters/http", File: "app/adapters/http"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if !strings.HasPrefix(insight.Title, "Constraint domain-reaches-contracts-only violated") {
			continue
		}
		if !strings.Contains(insight.Description, "the target names a measured fact") {
			t.Errorf("an exactly-named target must still report as one, got: %q", insight.Description)
		}
		return
	}
	t.Fatalf("no verdict to inspect: %+v", insights)
}

// private named a PATH as a non-exported member. A path is not a member: what
// the rule measured is that every fact the snapshot measured in that file is
// non-exported, which is a statement about the file.
func TestExplain_PrivateGroundedTargetIsNamedAsAFileNotAMember(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("views", "app/views/**"),
		componentIntent("js-controllers", "app/javascript/controllers/**"),
		ruleIntentProps("controllers-are-private", map[string]any{
			"private": "js-controllers", "because": "a controller's surface is its element, not its class"}),
		markupBinding("app/views/jobs/show.html.erb", "app/javascript/controllers/dropdown_controller.js"),
		facts.Fact{Kind: facts.KindSymbol, Name: "app/javascript/controllers.DropdownController",
			File:  "app/javascript/controllers/dropdown_controller.js",
			Props: map[string]any{"exported": false}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if !strings.HasPrefix(insight.Title, "Constraint controllers-are-private violated") {
			continue
		}
		if strings.Contains(insight.Description, "is a non-exported member of") {
			t.Errorf("the target is a path, and no fact carries it as a member name: %q", insight.Description)
		}
		if !strings.Contains(insight.Description, "is a measured file of") ||
			!strings.Contains(insight.Description, "whose every measured fact is non-exported") {
			t.Errorf("the verdict must state the file it grounded on, got: %q", insight.Description)
		}
		if strings.Contains(insight.Description, "membership is exact") {
			t.Errorf("a grounded membership was reported as exact: %q", insight.Description)
		}
		return
	}
	t.Fatalf("the binding reaches a file whose every measured fact is non-exported, so the rule must verdict: %+v", insights)
}

// The exact form of private keeps naming a member a member.
func TestExplain_PrivateExactTargetIsStillNamedAMember(t *testing.T) {
	store := privateStore(ruleIntentProps("pack-internals", map[string]any{
		"private": "billing", "because": "only the pack's public surface is a contract"}))
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if !strings.HasPrefix(insight.Title, "Constraint pack-internals violated") {
			continue
		}
		if !strings.Contains(insight.Description, "is a non-exported member of") ||
			!strings.Contains(insight.Description, "membership is exact") {
			t.Errorf("an exactly-named member must still report as one, got: %q", insight.Description)
		}
		return
	}
	t.Fatalf("no verdict to inspect: %+v", insights)
}

// protocol claimed "memberships are exact" for a step it reached only by
// grounding the edge's target on a measured file.
func TestExplain_ProtocolGroundedStepDoesNotClaimExactMemberships(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("flows", "app/checkout/**"),
		componentIntent("validate-cart", "app/steps/validate/**"),
		componentIntent("charge-payment", "app/steps/charge/**"),
		protocolRuleIntent("checkout-protocol", "flows", []string{"validate-cart", "charge-payment"}, "imports",
			"charging without validating charges garbage"),
		facts.Fact{Kind: facts.KindSymbol, Name: "Checkout::OrderFlow", File: "app/checkout/order_flow.js",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "app/steps/charge/charge_payment"}}},
		facts.Fact{Kind: facts.KindFileRef, Name: "app/steps/charge/charge_payment.js", File: "app/steps/charge/charge_payment.js"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Steps::ValidateCart", File: "app/steps/validate/validate_cart.js"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if !strings.HasPrefix(insight.Title, "Constraint checkout-protocol violated") {
			continue
		}
		if strings.Contains(insight.Description, "memberships are exact") {
			t.Errorf("the step was reached by grounding, not by an exact name: %q", insight.Description)
		}
		if !strings.Contains(insight.Description, "grounds on the measured file it names") {
			t.Errorf("the verdict must state that its step grounded, got: %q", insight.Description)
		}
		return
	}
	t.Fatalf("the member reaches the last step and none of the first, so the rule must verdict: %+v", insights)
}

// The exact form of protocol keeps its own sentence.
func TestExplain_ProtocolExactStepsStillClaimExactMemberships(t *testing.T) {
	insights, err := New().Explain(context.Background(), checkoutWorld())
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if !strings.HasPrefix(insight.Title, "Constraint checkout-protocol violated") {
			continue
		}
		if !strings.Contains(insight.Description, "both memberships are exact") {
			t.Errorf("every step was reached by an exact name, got: %q", insight.Description)
		}
		return
	}
	t.Fatalf("no verdict to inspect: %+v", insights)
}
