package engine_test

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// The extraction census decides, per (repo, file extension), whether a kind of
// edge is measured at all. That certificate excused the existential forms —
// require_edge and protocol — from the verdict-time reach screen on BOTH ends,
// while the reasoning behind the excuse only ever covered one: the end an edge
// LANDS on, where an empty resolution is the breach the form exists to report.
//
// On the source end the certificate is earned by facts the rule does not walk.
// A concept declaring owns: nothing sources its edges from the CLASS facts its
// predicate selects, while a Ruby class's calls ride its Owner#method facts —
// so the census says "calls are measured here", truthfully, about facts outside
// the rule's reach. The rule then reads its own empty source set as a verdict.
// Both readings are reproduced below, through the real extractors, and both are
// the same one-line cause.

func unownedConcept() intent.ConstraintComponent { return concept(intent.OwnsNothing, nil, "") }

func refusalNaming(insights []facts.Insight, fragment string) *facts.Insight {
	for i, insight := range insights {
		if strings.Contains(insight.Title, "cannot verdict") && strings.Contains(insight.Title, fragment) {
			return &insights[i]
		}
	}
	return nil
}

// An outbound require_edge over a concept owning nothing walks class facts that
// carry no calls edge, and reported every member as breaching the demand — a
// 1.0 verdict whose own sentence denied extraction blindness while the snapshot
// measured the very edge it demanded. It must refuse instead.
func TestCensusReach_AnOutboundDemandOverAnUnreachedSourceRefusesRatherThanBreaching(t *testing.T) {
	insights, err := snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(unownedConcept()),
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			RequireEdge: "exceptions", Direction: "outbound", Via: "calls", To: "models"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := breachWitnesses(insights); len(got) != 0 {
		t.Fatalf("breaches = %v, want none: the subject resolves no calls edge, so every member breaching is manufactured out of an empty resolution", got)
	}
	refusal := refusalNaming(insights, "require_edge role")
	if refusal == nil {
		t.Fatalf("insights = %v, want a stated refusal naming the require_edge role", unverdictableTitles(insights))
	}
	if refusal.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: a role that resolves nothing is not ambiguous", refusal.Confidence)
	}
}

// And the protocol half: the same declaration produced NO insight of any kind —
// neither verdict nor refusal — which reads exactly like a rule that ran and
// found nothing wrong.
func TestCensusReach_AProtocolOverAnUnreachedSourceSaysSoRatherThanGoingSilent(t *testing.T) {
	insights, err := snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(unownedConcept()),
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			Protocol: "exceptions", Steps: []string{"views", "models"}, Via: "calls"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if refusalNaming(insights, "protocol role") == nil {
		t.Fatalf("insights = %v, want a stated refusal naming the protocol role rather than silence", unverdictableTitles(insights))
	}
}

// The counterparty: the byte-identical declaration under owns: methods reaches
// the facts that carry the edges and finds the genuine breach. The refusal
// above is therefore about the declaration's reach and not about the estate.
func TestCensusReach_TheSameProtocolUnderOwnershipFindsTheBreach(t *testing.T) {
	insights, err := snapshotInsights(t, conceptRepo(t), declarationFile{
		Components: matrixComponents(owningConcept()),
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			Protocol: "exceptions", Steps: []string{"views", "models"}, Via: "calls"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := unverdictableTitles(insights); len(got) != 0 {
		t.Fatalf("refusals = %v, want none: owning its methods is what reaches the calls", got)
	}
	if got := breachWitnesses(insights); len(got) == 0 {
		t.Fatal("breaches = none, want the genuine breach the ownership declaration reaches")
	}
}

// The carve-out that survives, and must: an INBOUND demand's subject resolves
// against the end an edge lands on, where the empty resolution IS the breach.
// Screening it would silence exactly the total violation the form exists to
// catch, which is the fifth reproduction and stays fixed.
func TestCensusReach_AnInboundDemandStillReportsItsTotalViolation(t *testing.T) {
	insights, err := snapshotInsights(t, orphanRepo(t), declarationFile{
		Components: matrixComponents(owningConcept()),
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			RequireEdge: "exceptions", Direction: "inbound", Via: "calls"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := unverdictableTitles(insights); len(got) != 0 {
		t.Fatalf("refusals = %v, want none: the census answers for the landing end, and refusing here deletes the total violation", got)
	}
	want := "TimeoutError has no inbound calls edge"
	for _, witness := range breachWitnesses(insights) {
		if witness == want {
			return
		}
	}
	t.Fatalf("breaches = %v, want %q", breachWitnesses(insights), want)
}
