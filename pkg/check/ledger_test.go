package check

import (
	"strings"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

var ledgerNow = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

// ruleFact builds the compiled intent fact internal/intent emits for a rule,
// so the ledger is tested against the shape the explainer actually verdicts
// rather than a convenient one.
func ruleFact(id, mode, source, because, exempt string) facts.Fact {
	props := map[string]any{
		"intent_kind": "rule",
		"rule":        id,
		"mode":        mode,
		"because":     because,
		"source":      source,
	}
	if exempt != "" {
		props["exempt"] = exempt
	}
	return facts.Fact{Kind: facts.KindIntent, Name: "rule: " + id, File: source, Props: props}
}

func ledgerBreach(rule, witness string) facts.Insight {
	return facts.Insight{
		Title:      "Constraint " + rule + " violated: " + witness,
		Source:     "constraints",
		Confidence: 1.0,
	}
}

func storeWith(f ...facts.Fact) *facts.Store {
	s := facts.NewStore()
	s.Add(f...)
	return s
}

// A repository that declares no rules gets no ledger at all. Undeclared is
// unasked: a zeroed ledger would read as a law with nothing wrong with it.
func TestLedger_UndeclaredIsUnaskedNotClean(t *testing.T) {
	v := Evaluate(&diff.SnapshotDiff{}, Policy{})
	v = AttachLedger(v, storeWith(), Policy{}, nil, ledgerNow)
	if v.Law != nil {
		t.Fatalf("a repo declaring nothing must carry no ledger, got %+v", v.Law)
	}
	if line := v.Law.Line(); line != "" {
		t.Fatalf("nil ledger must render nothing, got %q", line)
	}
	if strings.Contains(v.Render(), "law:") {
		t.Fatalf("law line leaked into an undeclared verdict:\n%s", v.Render())
	}
}

// The headline: breaches raised, breaches excused, and the share between them,
// with both excuse mechanisms counted and neither one silently merged.
func TestLedger_CountsBothExcuseMechanisms(t *testing.T) {
	store := storeWith(
		ruleFact("company-fk", "ratchet", "enola/constraints/billing.yaml", "tenant isolation rides the FK",
			`[{"witness":"users must have fk_constraints","owner":"alice","because":"legacy tables migrate in Q4","since":"2026-01-01"}]`),
		ruleFact("domain-pure", "strict", "", "the domain must not know its delivery mechanisms", ""),
	)
	findings := []facts.Insight{
		ledgerBreach("company-fk", "orders must have fk_constraints"),
		ledgerBreach("company-fk", "invoices must have fk_constraints"),
		ledgerBreach("domain-pure", "domain -> adapters"),
		{Title: "Exempted from constraint company-fk: users must have fk_constraints", Source: "constraints", Confidence: 1.0},
		// Not a constraint finding at all — must not be counted as a breach.
		{Title: "Dependency cycle: a -> b -> a", Source: "cycles", Confidence: 1.0},
	}
	policy := Policy{Suppressions: []Suppression{
		{Rule: "domain-pure", Owner: "bob", Reason: "migration lands in Q4", Date: "2026-08-15"},
	}}

	led := ComputeLedger(store, policy.Suppressions, findings, ledgerNow)
	if led.Summary.Rules != 2 {
		t.Fatalf("rules = %d, want 2", led.Summary.Rules)
	}
	if led.Summary.Breaches != 3 {
		t.Fatalf("breaches = %d, want 3 (the cycle is not a constraint breach)", led.Summary.Breaches)
	}
	if led.Summary.Exempted != 1 {
		t.Fatalf("exempted = %d, want 1", led.Summary.Exempted)
	}
	if led.Summary.Suppressed != 1 {
		t.Fatalf("suppressed = %d, want 1", led.Summary.Suppressed)
	}
	if led.Summary.Excused != 2 {
		t.Fatalf("excused = %d, want 2", led.Summary.Excused)
	}
	// 2 excused over 4 raised (3 reported + 1 carved out).
	if got := led.Summary.ExcusedShare(); got != 0.5 {
		t.Fatalf("share = %v, want 0.5", got)
	}
	if led.Summary.ByMode["ratchet"] != 1 || led.Summary.ByMode["strict"] != 1 {
		t.Fatalf("mode breakdown = %v", led.Summary.ByMode)
	}
	// The oldest excuse is the exemption dated 2026-01-01, 236 days before the
	// fixed clock — not the suppression ten days old.
	if led.Summary.OldestExcuseDays != 236 {
		t.Fatalf("oldest excuse = %d days, want 236", led.Summary.OldestExcuseDays)
	}
}

// An exemption's carve-out is attributed to its own rule with the owner and
// reason it was signed with, because the point of the report is who to ask.
func TestLedger_ExcusesCarryTheirSignature(t *testing.T) {
	store := storeWith(ruleFact("company-fk", "ratchet", "", "",
		`[{"witness":"users must have fk_constraints","owner":"alice","because":"legacy tables migrate in Q4","since":"2026-01-01"}]`))
	led := ComputeLedger(store, []Suppression{
		{Rule: "company-fk", Owner: "bob", Reason: "second opinion", Date: "2026-08-24"},
	}, nil, ledgerNow)

	if len(led.Rules) != 1 || len(led.Rules[0].Excuses) != 2 {
		t.Fatalf("excuses = %+v", led.Rules)
	}
	var kinds, owners []string
	for _, ex := range led.Rules[0].Excuses {
		kinds = append(kinds, ex.Kind)
		owners = append(owners, ex.Owner)
	}
	// Sorted by kind, so the exemption leads.
	if strings.Join(kinds, ",") != "exemption,suppression" {
		t.Fatalf("kinds = %v, want exemption then suppression", kinds)
	}
	if strings.Join(owners, ",") != "alice,bob" {
		t.Fatalf("owners = %v", owners)
	}
	if led.Rules[0].Excuses[0].Reason != "legacy tables migrate in Q4" {
		t.Fatalf("exemption reason lost: %+v", led.Rules[0].Excuses[0])
	}
	if led.Rules[0].Excuses[1].AgeDays != 1 {
		t.Fatalf("suppression age = %d, want 1", led.Rules[0].Excuses[1].AgeDays)
	}
}

// An excuse with no readable date is counted as undatable rather than aged at
// zero: "signed yesterday" and "we cannot tell" must not render the same.
func TestLedger_UndatableExcuseIsNeverFresh(t *testing.T) {
	store := storeWith(ruleFact("company-fk", "ratchet", "", "",
		`[{"witness":"w","owner":"alice","because":"r","since":""}]`))
	led := ComputeLedger(store, nil, nil, ledgerNow)
	if led.Summary.UndatableExcuses != 1 {
		t.Fatalf("undatable = %d, want 1", led.Summary.UndatableExcuses)
	}
	if led.Summary.OldestExcuseDays != 0 {
		t.Fatalf("an undatable excuse must not set an age, got %d", led.Summary.OldestExcuseDays)
	}
	if !strings.Contains(led.Summary.Line(), "unreadable date") {
		t.Fatalf("line hides the undatable excuse: %q", led.Summary.Line())
	}
}

// A suppression selecting by title prefix is not attributed to a rule until it
// matches a finding — guessing which rule a free-text prefix meant is the
// inference this vocabulary refuses everywhere else.
func TestLedger_TitlePrefixSuppressionIsNotGuessedOntoARule(t *testing.T) {
	store := storeWith(ruleFact("company-fk", "ratchet", "", "", ""))
	sup := []Suppression{{FindingTitlePrefix: "Constraint company-fk", Owner: "a", Reason: "r", Date: "2026-08-01"}}
	led := ComputeLedger(store, sup, []facts.Insight{ledgerBreach("company-fk", "w")}, ledgerNow)

	if len(led.Rules[0].Excuses) != 0 {
		t.Fatalf("a prefix suppression must not be listed as a rule's own excuse: %+v", led.Rules[0].Excuses)
	}
	// It still suppresses the finding it matches, which is what gets counted.
	if led.Summary.Suppressed != 1 || led.Summary.Excused != 1 {
		t.Fatalf("suppressed=%d excused=%d, want 1 and 1", led.Summary.Suppressed, led.Summary.Excused)
	}
}

// The line renders beside the census on a real verdict, and a law nobody has
// had to excuse says so rather than printing a bare zero.
func TestLedger_LineRendersOnTheVerdict(t *testing.T) {
	store := storeWith(
		ruleFact("a", "ratchet", "", "", ""),
		ruleFact("b", "advisory", "", "", ""),
	)
	v := Evaluate(&diff.SnapshotDiff{}, Policy{})
	v = AttachLedger(v, store, Policy{}, []facts.Insight{ledgerBreach("a", "w")}, ledgerNow)
	out := v.Render()
	if !strings.Contains(out, "law: 2 rules (1 ratchet, 1 advisory) · 1 breach · none excused") {
		t.Fatalf("law line wrong:\n%s", out)
	}
}

// A single declared rule prints no mode breakdown — a repository with one mode
// should not have to read a parenthetical to learn it.
func TestLedger_ModeBreakdownOnlyWhenItDiscriminates(t *testing.T) {
	store := storeWith(ruleFact("a", "ratchet", "", "", ""), ruleFact("b", "ratchet", "", "", ""))
	led := ComputeLedger(store, nil, nil, ledgerNow)
	if got := led.Summary.Line(); got != "law: 2 rules · no breaches" {
		t.Fatalf("line = %q", got)
	}
}

// An excuse that excused nothing in this snapshot is marked idle. This is the
// row the report exists to surface: a signature standing over a breach that has
// since been fixed, moved, or stopped being selected.
func TestLedger_IdleExcusesAreNamed(t *testing.T) {
	store := storeWith(ruleFact("company-fk", "ratchet", "", "",
		`[{"witness":"live","owner":"alice","because":"r","since":"2026-01-01"},`+
			`{"witness":"stale","owner":"carol","because":"r","since":"2026-01-01"}]`))
	findings := []facts.Insight{
		{Title: "Exempted from constraint company-fk: live", Source: "constraints", Confidence: 1.0},
	}
	led := ComputeLedger(store, []Suppression{
		{Rule: "company-fk", Owner: "bob", Reason: "r", Date: "2026-08-01"},
	}, findings, ledgerNow)

	// The live exemption matched; the stale one and the suppression (which
	// silenced no finding) did not.
	if led.Summary.IdleExcuses != 2 {
		t.Fatalf("idle = %d, want 2", led.Summary.IdleExcuses)
	}
	byWitness := map[string]bool{}
	for _, ex := range led.Rules[0].Excuses {
		byWitness[ex.Kind+"/"+ex.Witness] = ex.Matched
	}
	if !byWitness["exemption/live"] {
		t.Fatalf("the applied exemption must be marked matched: %+v", led.Rules[0].Excuses)
	}
	if byWitness["exemption/stale"] || byWitness["suppression/"] {
		t.Fatalf("an excuse that excused nothing must not read as live: %+v", led.Rules[0].Excuses)
	}
	if !strings.Contains(led.Summary.Line(), "2 excuses matched nothing") {
		t.Fatalf("line hides idle excuses: %q", led.Summary.Line())
	}
}
