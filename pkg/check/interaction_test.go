package check

// The full interaction matrix over enforcement mode × suppression × baseline
// presence: {ratchet, strict, advisory, notify(guidance)} × {suppressed,
// unsuppressed} × {baselined, new}, each cell asserting the exact verdict
// bucket the finding lands in — or, for the combinations the vocabulary
// forbids, the validation error the declaration gets instead. The gate's
// semantics live in the interactions, and a cell nobody pinned is a cell that
// can drift.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// bucket names where a finding must land inside a Verdict.
const (
	inFailures   = "failures"
	inAdvisories = "advisories"
	inSuppressed = "suppressed"
	inNoBucket   = "absent" // delta-scoped away: reported nowhere, graded never
)

func constraintFinding(mode, ruleID string) facts.Insight {
	title := fmt.Sprintf("Constraint %s violated: a -> b via calls", ruleID)
	confidence := 1.0
	switch mode {
	case "strict":
		title = fmt.Sprintf("Strict constraint %s violated: a -> b via calls", ruleID)
	case "advisory":
		title = fmt.Sprintf("Advisory constraint %s violated: a -> b via calls", ruleID)
		confidence = 0.9
	}
	return facts.Insight{Title: title, Source: "constraints", Confidence: confidence}
}

func bucketOf(v Verdict, title string) string {
	for _, in := range v.Failures {
		if in.Title == title {
			return inFailures
		}
	}
	for _, in := range v.Advisories {
		if in.Title == title {
			return inAdvisories
		}
	}
	for _, in := range v.Suppressed {
		if in.Title == title {
			return inSuppressed
		}
	}
	return inNoBucket
}

func TestInteractionMatrix_ModeSuppressionBaseline(t *testing.T) {
	cases := []struct {
		mode       string
		suppressed bool
		baselined  bool
		wantBucket string
		wantStatus Status
	}{
		{"ratchet", false, false, inFailures, StatusRegression},
		{"ratchet", false, true, inNoBucket, StatusClean},
		{"ratchet", true, false, inSuppressed, StatusClean},
		{"ratchet", true, true, inNoBucket, StatusClean},

		// Strict is the one mode that opts out of delta scoping: a baselined
		// violation still fails, and the ledger is its only override.
		{"strict", false, false, inFailures, StatusRegression},
		{"strict", false, true, inFailures, StatusRegression},
		{"strict", true, false, inSuppressed, StatusClean},
		{"strict", true, true, inSuppressed, StatusClean},

		// Advisory reports below the gate's floor: visible when new, failing
		// never. A ledger entry still claims it — "someone signed this away"
		// and "below the policy" are different statements.
		{"advisory", false, false, inAdvisories, StatusClean},
		{"advisory", false, true, inNoBucket, StatusClean},
		{"advisory", true, false, inSuppressed, StatusClean},
		{"advisory", true, true, inNoBucket, StatusClean},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("%s/suppressed=%t/baselined=%t", tc.mode, tc.suppressed, tc.baselined)
		t.Run(name, func(t *testing.T) {
			ruleID := "cell-rule"
			finding := constraintFinding(tc.mode, ruleID)

			d := &diff.SnapshotDiff{}
			if !tc.baselined {
				d.FindingsNew = []facts.Insight{finding}
			}
			currentFindings := []facts.Insight{finding}

			policy := Policy{}
			if tc.suppressed {
				policy.Suppressions = []Suppression{{Rule: ruleID, Owner: "o", Reason: "r", Date: "2026-08-10"}}
			}

			v := EvaluateCurrent(d, policy, currentFindings)
			if got := bucketOf(v, finding.Title); got != tc.wantBucket {
				t.Fatalf("bucket = %s, want %s (verdict %+v)", got, tc.wantBucket, v.Status)
			}
			if v.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", v.Status, tc.wantStatus)
			}
		})
	}
}

// The notify row of the matrix is invalid by construction, in both
// directions: notify is guidance's channel and no enforcement mode, so a law
// form cannot declare it — and guidance cannot take an enforcement mode, so
// there exists no notify-mode finding to suppress or baseline. The cells
// assert the validation errors that make them unrepresentable.
func TestInteractionMatrix_NotifyCellsAreValidationErrors(t *testing.T) {
	notifyOnLaw := intent.ConstraintRule{ID: "r", Forbid: "c", To: "d", Via: "calls", Mode: "notify", Because: "x"}
	components := []intent.ConstraintComponent{
		{Name: "c", Match: []string{"a/**"}},
		{Name: "d", Match: []string{"b/**"}},
	}
	d := intent.Declaration{Components: components, Rules: []intent.ConstraintRule{notifyOnLaw}}
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "not an enforcement mode") {
		t.Fatalf("notify on a law form must be a named validation error, got: %v", err)
	}

	for _, mode := range []string{"ratchet", "strict"} {
		guide := intent.ConstraintRule{ID: "g", Guide: "c", Message: "m", Mode: mode, Because: "x"}
		d := intent.Declaration{Components: components, Rules: []intent.ConstraintRule{guide}}
		err := d.Validate()
		if err == nil || !strings.Contains(err.Error(), "not a guidance mode") {
			t.Fatalf("%s on a guidance rule must be a named validation error, got: %v", mode, err)
		}
	}
}
