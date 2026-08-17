package diff

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

const (
	thresholdCopy   = "a moving statistical threshold"
	decidedRuleCopy = "decided rules, not statistical thresholds"
)

func heuristicIncidental() facts.Insight {
	return facts.Insight{Source: "god-class", Title: "God class: Billing (fan-in 42)", Confidence: 0.7}
}

func constraintIncidental() facts.Insight {
	return facts.Insight{Source: "constraints", Title: "Constraint no-http violated: Billing -> HttpAdapter", Confidence: 1.0}
}

func TestIncidentalCopyForHeuristicFindingsKeepsThresholdDrift(t *testing.T) {
	d := &SnapshotDiff{FindingsNewIncidental: []facts.Insight{heuristicIncidental()}}
	out := d.RenderSummary()
	if !strings.Contains(out, thresholdCopy) {
		t.Errorf("heuristic incidental findings lost the threshold-drift explanation:\n%s", out)
	}
	if strings.Contains(out, decidedRuleCopy) {
		t.Errorf("decided-rule copy printed with no decided rule in the bucket:\n%s", out)
	}
}

func TestIncidentalCopyForDecidedRulesDropsThresholdDrift(t *testing.T) {
	d := &SnapshotDiff{FindingsNewIncidental: []facts.Insight{constraintIncidental()}}
	out := d.RenderSummary()
	if strings.Contains(out, thresholdCopy) {
		t.Errorf("a confidence-1.0 constraint verdict is explained as threshold drift:\n%s", out)
	}
	if !strings.Contains(out, decidedRuleCopy) {
		t.Errorf("no decided-rule explanation for a confidence-1.0 constraint verdict:\n%s", out)
	}
}

func TestIncidentalCopyForAMixedBucketCarriesBoth(t *testing.T) {
	d := &SnapshotDiff{
		FindingsNewIncidental:      []facts.Insight{heuristicIncidental()},
		FindingsResolvedIncidental: []facts.Insight{constraintIncidental()},
	}
	out := d.RenderSummary()
	if !strings.Contains(out, thresholdCopy) || !strings.Contains(out, decidedRuleCopy) {
		t.Errorf("a mixed bucket must explain each population in its own terms:\n%s", out)
	}
}

func TestIncidentalCopyTreatsAHeuristicConstraintAsHeuristic(t *testing.T) {
	advisory := facts.Insight{Source: "constraints", Title: "Constraint naming advisory", Confidence: 0.6}
	d := &SnapshotDiff{FindingsNewIncidental: []facts.Insight{advisory}}
	out := d.RenderSummary()
	if !strings.Contains(out, thresholdCopy) || strings.Contains(out, decidedRuleCopy) {
		t.Errorf("a sub-1.0 constraint finding is not a decided rule:\n%s", out)
	}
}

func TestIncidentalCopyRendersInCompactToo(t *testing.T) {
	d := &SnapshotDiff{FindingsNewIncidental: []facts.Insight{constraintIncidental()}}
	out := d.RenderCompact()
	if strings.Contains(out, thresholdCopy) || !strings.Contains(out, decidedRuleCopy) {
		t.Errorf("the compact view disagrees with the summary on decided rules:\n%s", out)
	}
}
