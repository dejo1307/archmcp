package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

func writeLedger(t *testing.T, content string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".enola")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "suppressions.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestLoadSuppressions_MissingFileIsAnEmptyLedger(t *testing.T) {
	got, err := LoadSuppressions(t.TempDir())
	if err != nil || got != nil {
		t.Fatalf("missing ledger = (%v, %v), want (nil, nil): suppressing nothing is the default, not an error", got, err)
	}
}

func TestLoadSuppressions_ValidLedgerParses(t *testing.T) {
	repo := writeLedger(t, `entries:
  - finding_title_prefix: "Call-graph hotspot: legacy.Router"
    owner: alice
    reason: "scheduled for the router rewrite"
    date: "2026-08-01"
  - rule: company-fk
    owner: bob
    reason: "legacy tables migrate in Q4"
    date: "2026-08-10"
`)
	got, err := LoadSuppressions(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].FindingTitlePrefix == "" || got[1].Rule != "company-fk" {
		t.Fatalf("ledger = %+v, want both entries with their selectors", got)
	}
}

// TestLoadSuppressions_StrictParsing: an unknown field, a selector-less entry,
// a double selector, or a missing accountability field each reject the whole
// ledger — half a ledger silences findings nobody signed off.
func TestLoadSuppressions_StrictParsing(t *testing.T) {
	cases := map[string]string{
		"unknown field": "entries:\n  - rule: r\n    owner: a\n    reason: x\n    date: \"2026-08-10\"\n    severity: high\n",
		"no selector":   "entries:\n  - owner: a\n    reason: x\n    date: \"2026-08-10\"\n",
		"two selectors": "entries:\n  - rule: r\n    finding_title_prefix: p\n    owner: a\n    reason: x\n    date: \"2026-08-10\"\n",
		"no owner":      "entries:\n  - rule: r\n    reason: x\n    date: \"2026-08-10\"\n",
		"no reason":     "entries:\n  - rule: r\n    owner: a\n    date: \"2026-08-10\"\n",
		"bad date":      "entries:\n  - rule: r\n    owner: a\n    reason: x\n    date: \"soon\"\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadSuppressions(writeLedger(t, content)); err == nil {
				t.Fatal("an invalid ledger must reject as a whole, got nil error")
			}
		})
	}
}

// TestEvaluateCurrent_BaselinedStrictViolationStillFails is strict mode's
// defining path: the violation is in the baseline, so the delta carries
// nothing new — and the gate fails anyway, because strict opts out of the
// ratchet's delta scoping.
func TestEvaluateCurrent_BaselinedStrictViolationStillFails(t *testing.T) {
	strict := insight("constraints", "Strict constraint company-fk violated: users must have fk_constraints containing company_id->companies", 1.0)
	d := &diff.SnapshotDiff{Comparability: diff.Comparability{Comparable: true}}

	v := EvaluateCurrent(d, Policy{}, []facts.Insight{strict})
	if v.Status != StatusRegression {
		t.Fatalf("status = %v, want regression: a strict violation fails even when baselined", v.Status)
	}
	if len(v.Failures) != 1 || v.Failures[0].Title != strict.Title {
		t.Fatalf("failures = %+v, want the strict violation", v.Failures)
	}

	if got := Evaluate(d, Policy{}); got.Status != StatusClean {
		t.Fatalf("delta-scoped Evaluate = %v, want clean: strict enforcement rides only the current-findings path", got.Status)
	}
}

// TestEvaluateCurrent_SuppressedStrictViolationReportsAndPasses is the
// ledger's path: the same baselined strict violation, excused by a signed
// entry, lands in the Suppressed bucket and fails nothing.
func TestEvaluateCurrent_SuppressedStrictViolationReportsAndPasses(t *testing.T) {
	strict := insight("constraints", "Strict constraint company-fk violated: users must have fk_constraints containing company_id->companies", 1.0)
	d := &diff.SnapshotDiff{Comparability: diff.Comparability{Comparable: true}}
	p := Policy{Suppressions: []Suppression{{Rule: "company-fk", Owner: "bob", Reason: "legacy tables migrate in Q4", Date: "2026-08-10"}}}

	v := EvaluateCurrent(d, p, []facts.Insight{strict})
	if v.Status != StatusClean {
		t.Fatalf("status = %v, want clean: a suppressed strict violation never fails", v.Status)
	}
	if len(v.Suppressed) != 1 || v.Suppressed[0].Title != strict.Title {
		t.Fatalf("suppressed = %+v, want the strict violation reported in its own bucket", v.Suppressed)
	}
	if len(v.Failures) != 0 || len(v.Advisories) != 0 {
		t.Fatalf("a suppressed finding must not leak into failures (%+v) or advisories (%+v)", v.Failures, v.Advisories)
	}
	if !strings.Contains(v.Render(), SuppressionsFileName) {
		t.Errorf("the rendered verdict must name the ledger so the signatures are findable:\n%s", v.Render())
	}
}

// TestEvaluateCurrent_NewStrictViolationGradedOnce: a strict violation that is
// ALSO new must fail exactly once, not once per pass.
func TestEvaluateCurrent_NewStrictViolationGradedOnce(t *testing.T) {
	strict := insight("constraints", "Strict constraint company-fk violated: users must have fk_constraints containing company_id->companies", 1.0)
	d := &diff.SnapshotDiff{
		Comparability: diff.Comparability{Comparable: true},
		FindingsNew:   []facts.Insight{strict},
	}
	v := EvaluateCurrent(d, Policy{}, []facts.Insight{strict})
	if v.Status != StatusRegression || len(v.Failures) != 1 {
		t.Fatalf("status = %v with %d failures, want regression with exactly 1", v.Status, len(v.Failures))
	}
}

// TestEvaluate_SuppressedNewFindingReportsAndPasses: the ledger applies to
// ordinary ratchet findings too — a new cycle a signed entry excuses reports
// in Suppressed and does not fail.
func TestEvaluate_SuppressedNewFindingReportsAndPasses(t *testing.T) {
	cycle := insight("cycles", "Cyclic dependency detected (2 modules)", 1.0)
	d := &diff.SnapshotDiff{
		Comparability: diff.Comparability{Comparable: true},
		FindingsNew:   []facts.Insight{cycle},
	}
	p := Policy{Suppressions: []Suppression{{FindingTitlePrefix: "Cyclic dependency detected", Owner: "alice", Reason: "splitting the module pair this sprint", Date: "2026-08-10"}}}
	v := Evaluate(d, p)
	if v.Status != StatusClean || len(v.Suppressed) != 1 || len(v.Failures) != 0 {
		t.Fatalf("verdict = %v (suppressed %d, failures %d), want clean with the cycle suppressed", v.Status, len(v.Suppressed), len(v.Failures))
	}
}

// TestSuppression_RuleSelectorMatchesEveryMode: a rule-id entry keeps matching
// when the rule's mode changes — ratchet, advisory and strict titles all
// resolve to the same declared rule.
func TestSuppression_RuleSelectorMatchesEveryMode(t *testing.T) {
	s := Suppression{Rule: "company-fk"}
	for _, title := range []string{
		"Constraint company-fk violated: x",
		"Advisory constraint company-fk violated: x",
		"Strict constraint company-fk violated: x",
	} {
		if !s.suppresses(insight("constraints", title, 1.0)) {
			t.Errorf("rule selector should match %q", title)
		}
	}
	if s.suppresses(insight("constraints", "Constraint company-fk-v2 violated: x", 1.0)) {
		t.Error("rule selector must not match a different rule id by prefix")
	}
	if s.suppresses(insight("cycles", "Constraint company-fk violated: x", 1.0)) {
		t.Error("rule selector must not match findings from another explainer")
	}
}
