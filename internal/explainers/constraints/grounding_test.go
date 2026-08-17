package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The shape finding 0010 was written about: lib/express.js requires
// ./application, and the extractor measures that as an imports edge onto the
// path lib/application — which names no member fact, because the file itself is
// measured as lib/application.js and file_ref is not a member kind. The rule
// yielded nothing on a dependency any reader of the file can see, and read
// exactly like the reverse rule, which is silent because the dependency is
// genuinely absent.
func TestExplain_FileGranularImportTargetGroundsInItsComponent(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("entry", "lib/express.js"),
		componentIntent("support", "lib/application.js"),
		ruleIntent("entry-avoids-support", "entry", "support", "imports", "the entry file must not reach the support files"),
		facts.Fact{Kind: facts.KindDependency, Name: "lib -> lib/application", File: "lib/express.js",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "lib/application"}}},
		facts.Fact{Kind: facts.KindFileRef, Name: "lib/application.js", File: "lib/application.js"},
		facts.Fact{Kind: facts.KindFileRef, Name: "lib/express.js", File: "lib/express.js"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var verdict *facts.Insight
	for i := range insights {
		if strings.HasPrefix(insights[i].Title, "Constraint entry-avoids-support violated") {
			verdict = &insights[i]
		}
	}
	if verdict == nil {
		t.Fatalf("the import is measured and the target names a measured file, so the rule must verdict: %+v", insights)
	}
	if verdict.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: the membership is the component's own match glob", verdict.Confidence)
	}
}

// The reverse direction stays silent for the reason it always did — there is no
// such import — so the fallback cannot be answering "yes" to everything.
func TestExplain_FileGranularFallbackDoesNotInventTheReverseEdge(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("entry", "lib/express.js"),
		componentIntent("support", "lib/application.js"),
		ruleIntent("support-avoids-entry", "support", "entry", "imports", "the support files must not reach back"),
		facts.Fact{Kind: facts.KindDependency, Name: "lib -> lib/application", File: "lib/express.js",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "lib/application"}}},
		facts.Fact{Kind: facts.KindFileRef, Name: "lib/application.js", File: "lib/application.js"},
		facts.Fact{Kind: facts.KindFileRef, Name: "lib/express.js", File: "lib/express.js"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if strings.Contains(insight.Title, "support-avoids-entry violated") {
			t.Fatalf("no such import is measured, yet the rule verdicted: %+v", insight)
		}
	}
}

// A target that names neither a fact nor a measured file — a package outside the
// tree — is skipped, and the skip is counted. Silence about it would read as
// compliance, which is the failure the advisory exists to prevent.
func TestExplain_UngroundableImportTargetsAreCounted(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("entry", "lib/express.js"),
		componentIntent("support", "lib/application.js"),
		ruleIntent("entry-avoids-support", "entry", "support", "imports", "the entry file must not reach the support files"),
		facts.Fact{Kind: facts.KindDependency, Name: "lib -> serve-static", File: "lib/express.js",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "serve-static"}}},
		facts.Fact{Kind: facts.KindFileRef, Name: "lib/application.js", File: "lib/application.js"},
		facts.Fact{Kind: facts.KindFileRef, Name: "lib/express.js", File: "lib/express.js"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var advisory *facts.Insight
	for i := range insights {
		if strings.Contains(insights[i].Title, "reached no verdict on 1 imports target") {
			advisory = &insights[i]
		}
		if strings.Contains(insights[i].Title, "entry-avoids-support violated") {
			t.Fatalf("an unresolvable target must never be guessed into a breach: %+v", insights[i])
		}
	}
	if advisory == nil {
		t.Fatalf("the skipped target must be counted, not dropped: %+v", insights)
	}
	if !strings.Contains(advisory.Description, "serve-static") {
		t.Errorf("the advisory must name the sample it counted, got: %q", advisory.Description)
	}
	if advisory.Confidence >= 1.0 {
		t.Errorf("confidence = %v, want a skip advisory below the gate floor", advisory.Confidence)
	}
}

// Directory imports resolve to their index file, and the resolution is scoped to
// the importing repository: a file measured in one repo can never ground a
// target written in another.
func TestGrounding_ResolvesIndexAndStaysInsideTheRepo(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindFileRef, Repo: "a", Name: "src/widgets/index.ts", File: "a/src/widgets/index.ts"},
		facts.Fact{Kind: facts.KindFileRef, Repo: "b", Name: "src/other.ts", File: "b/src/other.ts"},
	)
	g := newGrounding(store, nil)
	if got, ok := g.resolve("src/widgets", "a"); !ok || got != "a/src/widgets/index.ts" {
		t.Errorf("resolve(src/widgets, a) = %q/%v, want the index file", got, ok)
	}
	if _, ok := g.resolve("src/other", "a"); ok {
		t.Error("a file measured in repo b must not ground a target written in repo a")
	}
	if _, ok := g.resolve("src/missing", "a"); ok {
		t.Error("a target naming no measured file must not resolve")
	}
}

// markupBinding is the shape the Ruby extractor's Stimulus pass emits for
// `data-controller="dropdown"` in an ERB view: a dependency fact filed under
// the VIEW, carrying resolution_level=markup-declared, whose depends_on target
// is the controller FILE the naming convention resolved — a path, never a fact
// name, exactly like a file-granular import target.
func markupBinding(view, controllerFile string) facts.Fact {
	return facts.Fact{Kind: facts.KindDependency, Name: "stimulus-binding: " + view + " -> dropdown", File: view,
		Props:     map[string]any{"framework": "stimulus", "resolution_level": "markup-declared"},
		Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: controllerFile}}}
}

// The Stimulus half of finding 0007 measured the binding and finding 0010's
// grounding admitted only imports edges, so a rule over the markup-declared
// depends_on edge held vacuously: the target is a controller file path, which
// names no member fact. It must now ground on the component that file belongs
// to, exactly as an import target does.
func TestExplain_MarkupDeclaredBindingGroundsInItsComponent(t *testing.T) {
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
	var verdict *facts.Insight
	for i := range insights {
		if strings.HasPrefix(insights[i].Title, "Constraint views-avoid-controllers violated") {
			verdict = &insights[i]
		}
	}
	if verdict == nil {
		t.Fatalf("the binding is measured and its target names a measured file, so the rule must verdict: %+v", insights)
	}
	if verdict.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: the membership is the component's own match glob", verdict.Confidence)
	}
}

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
		if !strings.Contains(insight.Description, "grounds on the measured file its edge names") {
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
		if !strings.Contains(insight.Description, "both memberships are exact") {
			t.Errorf("the target names a member fact exactly, so the verdict must report an exact membership, got: %q", insight.Description)
		}
		return
	}
	t.Fatalf("a target naming a member exactly must still verdict: %+v", insights)
}

// A depends_on edge the extractors did NOT declare in markup keeps naming a
// symbol — an association, a Terraform address, a render target — so it must not
// be resolved as a path. The literal-declared render edge is the near neighbor
// that would break first: its target IS a file, and admitting it is a separate
// decision this predicate does not make.
func TestExplain_NonMarkupDependsOnTargetIsNotGrounded(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("views", "app/views/**"),
		componentIntent("partials", "app/views/shared/**"),
		ruleIntent("views-avoid-partials", "views", "partials", "depends_on", "a view must not render a shared partial"),
		facts.Fact{Kind: facts.KindDependency, Name: "render: app/views/jobs/show.html.erb -> shared/help", File: "app/views/jobs/show.html.erb",
			Props:     map[string]any{"framework": "rails", "resolution_level": "literal-declared"},
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: "app/views/shared/_help.html.erb"}}},
		facts.Fact{Kind: facts.KindFileRef, Name: "app/views/shared/_help.html.erb", File: "app/views/shared/_help.html.erb"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, insight := range insights {
		if strings.HasPrefix(insight.Title, "Constraint views-avoid-partials violated") {
			t.Fatalf("a depends_on edge that was not declared in markup must not ground as a path: %+v", insight)
		}
	}
}

// The predicate itself, at the unit: what it admits and what it refuses. A
// markup-declared fact's OTHER edge kinds stay out too — the level says how the
// fact was resolved, never that every edge it carries is a path.
func TestPathTargetEdge_AdmitsImportsAndMarkupDependsOnOnly(t *testing.T) {
	markup := facts.Fact{Props: map[string]any{"resolution_level": "markup-declared"}}
	literal := facts.Fact{Props: map[string]any{"resolution_level": "literal-declared"}}
	plain := facts.Fact{}
	cases := []struct {
		name string
		rel  facts.Relation
		from facts.Fact
		want bool
	}{
		{"an imports edge is always a path target", facts.Relation{Kind: facts.RelImports}, plain, true},
		{"a markup-declared depends_on edge is a path target", facts.Relation{Kind: facts.RelDependsOn}, markup, true},
		{"a literal-declared depends_on edge is not", facts.Relation{Kind: facts.RelDependsOn}, literal, false},
		{"an unlevelled depends_on edge is not", facts.Relation{Kind: facts.RelDependsOn}, plain, false},
		{"a markup-declared calls edge is not", facts.Relation{Kind: facts.RelCalls}, markup, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathTargetEdge(tc.rel, tc.from); got != tc.want {
				t.Errorf("pathTargetEdge = %v, want %v", got, tc.want)
			}
		})
	}
}

// The skip advisory reached the markup edge with an importer's diagnosis: a
// binding whose target resolved to nothing is an IN-TREE application path the
// naming convention produced, so calling it a package outside the tree names the
// wrong thing, and neither remedy an import's residue takes can reach it —
// ignoring third-party packages does not apply, and no snapshot is wide enough
// to contain a path the tree already holds.
func TestExplain_MarkupSkipAdvisoryDiagnosesAnInTreePath(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("views", "app/views/**"),
		componentIntent("js-controllers", "app/javascript/controllers/**"),
		ruleIntent("views-avoid-controllers", "views", "js-controllers", "depends_on", "a view must not bind a controller directly"),
		markupBinding("app/views/dashboard/show.html.erb", "app/javascript/controllers/admin/menu_controller.js"),
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var advisory *facts.Insight
	for i := range insights {
		if strings.Contains(insights[i].Title, "reached no verdict on 1 depends_on target") {
			advisory = &insights[i]
		}
		if strings.Contains(insights[i].Title, "views-avoid-controllers violated") {
			t.Fatalf("a target naming no measured file must never be guessed into a breach: %+v", insights[i])
		}
	}
	if advisory == nil {
		t.Fatalf("the skipped binding must be counted, not dropped: %+v", insights)
	}
	if strings.Contains(advisory.Description, "package outside the tree") {
		t.Errorf("a markup-declared target is in-tree by construction, got: %q", advisory.Description)
	}
	if !strings.Contains(advisory.Description, "in-tree path the declaring markup's naming convention produced") {
		t.Errorf("the advisory must say what the target actually is, got: %q", advisory.Description)
	}
	for _, action := range advisory.Actions {
		if strings.Contains(action, "third-party packages") || strings.Contains(action, "Widen the snapshot") {
			t.Errorf("an import's remedy cannot reach an in-tree path, got action: %q", action)
		}
	}
	if len(advisory.Actions) != 2 {
		t.Fatalf("actions = %+v, want the two a markup-declared residue has", advisory.Actions)
	}
	if !strings.Contains(advisory.Actions[0], "Fix the binding") || !strings.Contains(advisory.Actions[1], "Extend extraction") {
		t.Errorf("actions = %+v, want the binding and the extraction levers", advisory.Actions)
	}
}

// The imports diagnosis is the one this advisory was written for and must not
// move: a third-party package IS the common residue there, and the snapshot IS
// the lever. Two situations, two diagnoses.
func TestGroundSkipDiagnosis_ImportsAndMarkupDiagnoseDifferently(t *testing.T) {
	importsCause, importsActions := groundSkipDiagnosis(facts.RelImports)
	if importsCause != "typically a package outside the tree" {
		t.Errorf("imports cause = %q, want the package-outside-the-tree diagnosis", importsCause)
	}
	if len(importsActions) != 2 || !strings.Contains(importsActions[1], "Widen the snapshot") {
		t.Errorf("imports actions = %+v, want the third-party and snapshot levers", importsActions)
	}
	markupCause, markupActions := groundSkipDiagnosis(facts.RelDependsOn)
	if markupCause == importsCause {
		t.Errorf("both edge kinds got one diagnosis: %q", markupCause)
	}
	if strings.Contains(markupCause, "outside the tree") {
		t.Errorf("markup cause = %q, want an in-tree diagnosis", markupCause)
	}
	for i, action := range markupActions {
		if action == importsActions[i] {
			t.Errorf("markup remedy %d is the import's: %q", i, action)
		}
	}
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
		if !strings.Contains(insight.Description, "grounds on the measured file its edge names") {
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
