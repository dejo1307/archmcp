package diff

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

func exemptedRule(id string, form map[string]any, witnesses ...string) facts.Fact {
	ex := make([]intent.ConstraintExemption, 0, len(witnesses))
	for _, w := range witnesses {
		ex = append(ex, intent.ConstraintExemption{
			Witness: w, Owner: "platform", Because: "scheduled separately", Since: "2026-08-13",
		})
	}
	props := map[string]any{}
	for k, v := range form {
		props[k] = v
	}
	props["exempt"] = intent.EncodeExemptions(ex)
	return ruleFact(id, props)
}

// The inverse silencing, and the ordinary one: it steals credit for work someone
// did. declarationIdentity compared the rule's props as one blob, exemptions
// included, so adding a carve-out for witness X and genuinely FIXING witness Y
// in the same change filed Y under "the breaching code is unchanged; the law
// stopped asking". Y's code was changed, by the person reading that sentence.
//
// Exemptions are compared per witness now, so X is undeclared and Y is resolved.
func TestCompute_AnExemptionForOneWitnessDoesNotUnclaimAnotherWitnessFix(t *testing.T) {
	component := componentFact("errors", map[string]string{"superclass": "StandardError"})
	form := map[string]any{"require_name": "errors", "pattern": "*Error"}
	excused := constraintInsight("Constraint errors-are-recognisable violated: Failed does not match *Error", "Failed")
	fixed := constraintInsight("Constraint errors-are-recognisable violated: Broken does not match *Error", "Broken")

	base := snap([]facts.Fact{component, ruleFact("errors-are-recognisable", form),
		errorClass("Failed", "StandardError"), errorClass("Broken", "StandardError")},
		[]facts.Insight{excused, fixed})
	cur := snap([]facts.Fact{component,
		exemptedRule("errors-are-recognisable", form, "Failed does not match *Error"),
		errorClass("Failed", "StandardError"), errorClass("BrokenError", "StandardError")}, nil)

	d := Compute(base, cur)
	if len(d.FindingsUndeclared) != 1 || d.FindingsUndeclared[0].Title != excused.Title {
		t.Fatalf("FindingsUndeclared = %+v, want only the witness the exemption named", d.FindingsUndeclared)
	}
	if len(d.FindingsResolved) != 1 || d.FindingsResolved[0].Title != fixed.Title {
		t.Fatalf("FindingsResolved = %+v, want the witness the change actually fixed", d.FindingsResolved)
	}
}

// Moving a rule to another constraints file, or relabelling the recipe instance
// that expanded it, changes no term of what the rule judges — and both put every
// breach the same change fixed into "the law stopped asking". The identity reads
// the law now, not its bookkeeping.
func TestCompute_MovingARuleBetweenFilesDoesNotUnclaimAFix(t *testing.T) {
	component := componentFact("errors", map[string]string{"superclass": "StandardError"})
	form := map[string]any{"require_name": "errors", "pattern": "*Error"}
	breach := constraintInsight("Constraint errors-are-recognisable violated: Failed does not match *Error", "Failed")

	moved := ruleFact("errors-are-recognisable", form)
	moved.Props["source"] = "enola/constraints/naming.yaml"
	moved.Props["recipe"] = "layered"
	moved.Props["instance"] = "errors-v2"

	d := Compute(
		snap([]facts.Fact{component, ruleFact("errors-are-recognisable", form), errorClass("Failed", "StandardError")},
			[]facts.Insight{breach}),
		snap([]facts.Fact{component, moved, errorClass("FailedError", "StandardError")}, nil))

	if len(d.FindingsUndeclared) != 0 {
		t.Errorf("FindingsUndeclared = %+v, want none: nothing the rule judges changed", d.FindingsUndeclared)
	}
	if len(d.FindingsResolved) != 1 || d.FindingsResolved[0].Title != breach.Title {
		t.Fatalf("FindingsResolved = %+v, want the fix credited to the change that made it", d.FindingsResolved)
	}
}

// Changing a term of the law still counts. The exclusions are bookkeeping only,
// and a fix credited to a change that quietly narrowed the pattern would be the
// same theft in the other direction.
func TestCompute_ChangingATermOfTheLawIsStillADeclarationChange(t *testing.T) {
	component := componentFact("errors", map[string]string{"superclass": "StandardError"})
	breach := constraintInsight("Constraint errors-are-recognisable violated: Failed does not match *Error", "Failed")

	d := Compute(
		snap([]facts.Fact{component, ruleFact("errors-are-recognisable", map[string]any{"require_name": "errors", "pattern": "*Error"}),
			errorClass("Failed", "StandardError")}, []facts.Insight{breach}),
		snap([]facts.Fact{component, ruleFact("errors-are-recognisable", map[string]any{"require_name": "errors", "pattern": "*"}),
			errorClass("Failed", "StandardError")}, nil))

	if len(d.FindingsUndeclared) != 1 {
		t.Fatalf("FindingsUndeclared = %+v, want the breach the widened pattern stopped reporting", d.FindingsUndeclared)
	}
}

// Silencing, case four: a repository dropped from a union snapshot. Every
// verdict about its code goes quiet at once, and the witness stops being
// measured — which is exactly the shape of deleted code, so byMembershipChange
// skips it and byDeclarationChange is false because the rule is byte-identical.
// The result was PASS, exit 0, and an unchanged still-breaching witness printed
// under "Resolved by this change".
func TestCompute_ARepoDroppedFromAUnionDoesNotResolveItsBreaches(t *testing.T) {
	component := componentFact("errors", map[string]string{"superclass": "StandardError"})
	rule := ruleFact("errors-are-recognisable", map[string]any{"require_name": "errors", "pattern": "*Error"})
	breach := constraintInsight("Constraint errors-are-recognisable violated: Failed does not match *Error", "Failed")
	other := facts.Fact{Kind: facts.KindSymbol, Name: "Kept", File: "app/kept.rb", Repo: "other",
		Props: map[string]any{"symbol_kind": facts.SymbolClass}}

	base := snap([]facts.Fact{component, rule, errorClass("Failed", "StandardError"), other}, []facts.Insight{breach})
	cur := snap([]facts.Fact{component, rule, other}, nil)

	d := Compute(base, cur)
	if len(d.FindingsResolved) != 0 {
		t.Errorf("FindingsResolved = %+v, want none: the repo left the snapshot, the code did not change", d.FindingsResolved)
	}
	if len(d.FindingsUnattributed) != 1 || d.FindingsUnattributed[0].Title != breach.Title {
		t.Fatalf("FindingsUnattributed = %+v, want the breach whose repository is gone", d.FindingsUnattributed)
	}
	if !d.Comparability.HasKind(WarnUnionMembership) {
		t.Errorf("comparability = %+v, want the dropped-member warning: WarnDifferentRepo keys on the snapshot's own identity, which a union's members are not", d.Comparability)
	}
	if d.Comparability.Comparable {
		t.Error("Comparable = true — a union that lost a member is not comparable with one that had it")
	}
	var named bool
	for _, w := range d.Comparability.Warnings {
		if strings.Contains(w, "r") {
			named = true
		}
	}
	if !named {
		t.Errorf("warnings = %v, want the missing repo named", d.Comparability.Warnings)
	}
}

// Deleting the code inside a repository the snapshot still measures is a real
// resolution and must stay one. The repo arm is about a repo that left, not
// about any unmeasured witness.
func TestCompute_DeletedCodeInsideAMeasuredRepoIsStillResolved(t *testing.T) {
	component := componentFact("errors", map[string]string{"superclass": "StandardError"})
	rule := ruleFact("errors-are-recognisable", map[string]any{"require_name": "errors", "pattern": "*Error"})
	breach := constraintInsight("Constraint errors-are-recognisable violated: Failed does not match *Error", "Failed")

	d := Compute(
		snap([]facts.Fact{component, rule, errorClass("Failed", "StandardError"), errorClass("KeptError", "StandardError")},
			[]facts.Insight{breach}),
		snap([]facts.Fact{component, rule, errorClass("KeptError", "StandardError")}, nil))

	if len(d.FindingsUnattributed) != 0 {
		t.Errorf("FindingsUnattributed = %+v, want none: repo r is still measured", d.FindingsUnattributed)
	}
	if len(d.FindingsResolved) != 1 {
		t.Fatalf("FindingsResolved = %+v, want the deleted class's breach", d.FindingsResolved)
	}
}

// A baseline carrying constraint findings with no declaration for the rule that
// produced them cannot be compared against at all — neither silencing test can
// run — and falling through both into Resolved credits the change with clearing
// something the snapshot never established was there.
func TestCompute_ABaselineWithNoDeclarationCannotCreditAFix(t *testing.T) {
	breach := constraintInsight("Constraint errors-are-recognisable violated: Failed does not match *Error", "Failed")
	base := snap([]facts.Fact{errorClass("Failed", "StandardError")}, []facts.Insight{breach})
	cur := snap([]facts.Fact{errorClass("FailedError", "StandardError")}, nil)

	d := Compute(base, cur)
	if len(d.FindingsResolved) != 0 {
		t.Errorf("FindingsResolved = %+v, want none: the baseline declared no such rule to compare against", d.FindingsResolved)
	}
	if len(d.FindingsUnattributed) != 1 {
		t.Fatalf("FindingsUnattributed = %+v, want the finding whose declaration the baseline never carried", d.FindingsUnattributed)
	}
}
