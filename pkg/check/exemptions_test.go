package check

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

func exemptedInsight() facts.Insight {
	in := insight("constraints", "Exempted from constraint company-fk: legacy_imports must have fk_constraints containing company_id->companies", 0.9)
	in.Evidence = []facts.Evidence{{Fact: "rule: company-fk", Detail: "exempted by dana since 2026-08-01 — legacy_imports keys company_id to the archived companies snapshot, not companies"}}
	return in
}

func TestEvaluateCurrent_NewExemptedFindingLandsInItsOwnBucket(t *testing.T) {
	d := &diff.SnapshotDiff{
		Comparability: diff.Comparability{Comparable: true},
		FindingsNew:   []facts.Insight{exemptedInsight()},
	}
	v := EvaluateCurrent(d, Policy{}, []facts.Insight{exemptedInsight()})
	if v.Status != StatusClean {
		t.Fatalf("status = %v, want clean: an exemption is a declared decision, never a failure", v.Status)
	}
	if len(v.Exempted) != 1 || v.Exempted[0].Title != exemptedInsight().Title {
		t.Fatalf("exempted = %+v, want the entry once in its own bucket", v.Exempted)
	}
	if len(v.Failures) != 0 || len(v.Advisories) != 0 || len(v.Suppressed) != 0 {
		t.Fatalf("an exempted finding must not leak into failures (%+v), advisories (%+v) or suppressed (%+v)", v.Failures, v.Advisories, v.Suppressed)
	}
}

func TestEvaluateCurrent_BaselinedExemptedFindingStillReports(t *testing.T) {
	d := &diff.SnapshotDiff{Comparability: diff.Comparability{Comparable: true}}
	v := EvaluateCurrent(d, Policy{}, []facts.Insight{exemptedInsight()})
	if v.Status != StatusClean {
		t.Fatalf("status = %v, want clean", v.Status)
	}
	if len(v.Exempted) != 1 {
		t.Fatalf("exempted = %+v, want the standing exemption reported on every run, never silent", v.Exempted)
	}
	rendered := v.Render()
	if !strings.Contains(rendered, "Exempted by declaration (1)") {
		t.Errorf("the rendered verdict must count the exemptions:\n%s", rendered)
	}
	if !strings.Contains(rendered, "exempted by dana since 2026-08-01") {
		t.Errorf("the rendered verdict must carry the exemption's signature and reason:\n%s", rendered)
	}
}

func TestEvaluateCurrent_InstancePrefixedExemptionLandsInTheExemptedBucket(t *testing.T) {
	in := insight("constraints", "Exempted from constraint orders-events/events-consumed: LegacyOrderMigratedEvent has no inbound calls edge from orders-events/handlers", 0.9)
	in.Evidence = []facts.Evidence{{Fact: "rule: orders-events/events-consumed", Detail: "exempted by dana since 2026-08-11 — fired only by the migration backfill, consumed manually"}}
	d := &diff.SnapshotDiff{
		Comparability: diff.Comparability{Comparable: true},
		FindingsNew:   []facts.Insight{in},
	}
	v := EvaluateCurrent(d, Policy{}, []facts.Insight{in})
	if v.Status != StatusClean || len(v.Failures) != 0 {
		t.Fatalf("status = %v with failures %+v, want clean: a recipe-expanded exemption is an ordinary exemption", v.Status, v.Failures)
	}
	if len(v.Exempted) != 1 || v.Exempted[0].Title != in.Title {
		t.Fatalf("exempted = %+v, want the instance-prefixed entry once in its own bucket", v.Exempted)
	}
}

func TestEvaluate_ExemptedFindingNeverFailsUnderAnyPolicy(t *testing.T) {
	d := &diff.SnapshotDiff{
		Comparability: diff.Comparability{Comparable: true},
		FindingsNew:   []facts.Insight{exemptedInsight()},
	}
	v := Evaluate(d, Policy{MinConfidence: 0.5})
	if v.Status != StatusClean || len(v.Failures) != 0 {
		t.Fatalf("verdict = %v with failures %+v, want clean: the bucket routing must win before any confidence policy", v.Status, v.Failures)
	}
	if len(v.Exempted) != 1 {
		t.Fatalf("exempted = %+v, want the entry bucketed from the delta path too", v.Exempted)
	}
}

// A breach whose witness this delta carves out is reported once, as excused.
// It reaches the undeclared bucket honestly — an exemption IS a declaration
// change — but the gate printed the same witness under two headings, and one
// of them said nothing the other did not.
func TestEvaluateCurrent_AnExemptedBreachIsNotAlsoReportedAsUndeclared(t *testing.T) {
	carveOut := insight("constraints", "Exempted from constraint errors-are-recognisable: Failed does not match *Error", 0.9)
	breach := insight("constraints", "Constraint errors-are-recognisable violated: Failed does not match *Error", 1.0)
	other := insight("constraints", "Constraint errors-are-recognisable violated: Broken does not match *Error", 1.0)
	d := &diff.SnapshotDiff{
		Comparability:      diff.Comparability{Comparable: true},
		FindingsNew:        []facts.Insight{carveOut},
		FindingsUndeclared: []facts.Insight{breach, other},
	}
	v := EvaluateCurrent(d, Policy{}, []facts.Insight{carveOut})
	if len(v.Exempted) != 1 {
		t.Fatalf("exempted = %+v, want the carve-out", v.Exempted)
	}
	if len(v.Undeclared) != 1 || v.Undeclared[0].Title != other.Title {
		t.Fatalf("undeclared = %+v, want only the breach nobody excused", v.Undeclared)
	}
	rendered := v.Render()
	if strings.Count(rendered, "Failed does not match *Error") != 1 {
		t.Errorf("the excused witness is printed more than once:\n%s", rendered)
	}
	if !strings.Contains(rendered, "No longer declared (1)") {
		t.Errorf("the unexcused breach must still be reported as undeclared:\n%s", rendered)
	}
}

// A delta whose only content is a breach that stopped being declared is not
// "no architectural change": the headline is the line a reader skims, and it
// contradicted the section printed under it.
func TestEvaluateCurrent_UndeclaredBreachChangesTheHeadline(t *testing.T) {
	breach := insight("constraints", "Constraint errors-are-recognisable violated: Failed does not match *Error", 1.0)
	d := &diff.SnapshotDiff{
		Comparability:      diff.Comparability{Comparable: true},
		FindingsUndeclared: []facts.Insight{breach},
	}
	rendered := EvaluateCurrent(d, Policy{}, nil).Render()
	if strings.Contains(rendered, "no architectural change") {
		t.Errorf("headline claims no architectural change above a no-longer-declared breach:\n%s", rendered)
	}
}
