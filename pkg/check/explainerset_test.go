package check

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

// A differing explainer set must NOT decline the grade. The fact delta is untouched by
// it, so the change is still gradeable; only the findings from the explainers that
// differ are misattributed. Declining here would cost a red build for a config edit.
func TestEvaluate_ExplainerSetIsAdvisoryNotBlocking(t *testing.T) {
	d := &diff.SnapshotDiff{
		Comparability: diff.Comparability{
			Comparable: false,
			Kinds:      []diff.WarningKind{diff.WarnExplainerSet},
			Warnings:   []string{"current run added explainer(s) not in the baseline: dead-code"},
		},
	}

	v := Evaluate(d, Policy{})

	if v.Status == StatusIncomparable {
		t.Errorf("Status = %q, want a graded status — an explainer mismatch must not decline", v.Status)
	}
	if len(v.BlockingKinds) != 0 {
		t.Errorf("BlockingKinds = %v, want none", v.BlockingKinds)
	}
	if len(v.AdvisoryKinds) != 1 || v.AdvisoryKinds[0] != diff.WarnExplainerSet {
		t.Errorf("AdvisoryKinds = %v, want [%v]", v.AdvisoryKinds, diff.WarnExplainerSet)
	}
}

// Advisory must not mean silent. The warning text renders from ComparabilityWarnings
// whatever the kind is, so asserting only on that would pass without the kind being
// registered at all. What registration buys is the categorised summary — the line that
// tells a reader this was graded anyway, and why that is safe here.
func TestRender_ExplainerSetIsCategorisedAsAdvisory(t *testing.T) {
	const warning = "current run added explainer(s) not in the baseline: dead-code — their findings will all appear as NEW"
	d := &diff.SnapshotDiff{
		Comparability: diff.Comparability{
			Comparable: false,
			Kinds:      []diff.WarningKind{diff.WarnExplainerSet},
			Warnings:   []string{warning},
		},
	}

	out := Evaluate(d, Policy{}).Render()

	if !strings.Contains(out, "dead-code") {
		t.Errorf("rendered verdict does not mention the differing explainer:\n%s", out)
	}
	if !strings.Contains(out, "Advisory (graded anyway)") {
		t.Errorf("explainer mismatch not categorised as advisory:\n%s", out)
	}
	if !strings.Contains(out, string(diff.WarnExplainerSet)) {
		t.Errorf("advisory summary does not name the %s kind:\n%s", diff.WarnExplainerSet, out)
	}
	// The kind must carry a meaning; writeKinds falls back to printing the bare kind
	// name, which tells a reader nothing about why the grade was still trustworthy.
	if !strings.Contains(out, "the facts and coupling in this delta are unaffected") {
		t.Errorf("advisory summary has no meaning text for %s:\n%s", diff.WarnExplainerSet, out)
	}
}

// The gate still fails on a real regression while the advisory is in force — the
// advisory softens the comparability caveat, not the policy.
func TestEvaluate_ExplainerSetStillGradesRegressions(t *testing.T) {
	d := &diff.SnapshotDiff{
		Comparability: diff.Comparability{
			Comparable: false,
			Kinds:      []diff.WarningKind{diff.WarnExplainerSet},
			Warnings:   []string{"explainer sets differ"},
		},
		FindingsNew: []facts.Insight{{
			Source: "cycles", Title: "Cyclic dependency detected (2 modules)", Confidence: 1.0,
		}},
	}

	v := Evaluate(d, Policy{})

	if v.Status != StatusRegression {
		t.Errorf("Status = %q, want %q", v.Status, StatusRegression)
	}
	if v.ExitCode() != 1 {
		t.Errorf("ExitCode = %d, want 1", v.ExitCode())
	}
}
