package check

import (
	"strings"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/facts"
)

func dated(title, since string) facts.Insight {
	return facts.Insight{Title: title, Source: "constraints", Confidence: 1, Evidence: []facts.Evidence{{Fact: "since: " + since}}}
}

// The architecture history annotates a dated breach and never grades it. With no
// witness date available nothing ratchets, whatever the revision carried — but each
// breach still says what the revision showed.
func TestApplyTime_SinceAnnotatesFromTheRevisionButDoesNotGrade(t *testing.T) {
	old := dated("Constraint jobs-stay-off-controllers violated: OldJob -> AppController via calls", "2026-08-01")
	fresh := dated("Constraint jobs-stay-off-controllers violated: NewJob -> AppController via calls", "2026-08-01")
	early := dated("Constraint models-stay-pure violated: Order -> AppController via calls", "2020-01-01")
	v := Verdict{Status: StatusRegression, Failures: []facts.Insight{old, fresh, early}}
	history := func(date time.Time) (*facts.Snapshot, string, string, bool) {
		if date.Year() < 2026 {
			return nil, "", "2026-07-15", false
		}
		return &facts.Snapshot{Insights: []facts.Insight{{Title: old.Title}}}, "2026-07-31", "2026-07-15", true
	}
	out := ApplyTime(v, nil, history, nil)

	if len(out.Failures) != 3 || len(out.Advisories) != 0 {
		t.Fatalf("the history grades nothing: failures=%d advisories=%d", len(out.Failures), len(out.Advisories))
	}
	if !strings.Contains(out.Failures[0].Description, "Present in the revision of 2026-07-31") ||
		!strings.Contains(out.Failures[0].Description, "reported, not graded") {
		t.Errorf("carried breach must say so: %q", out.Failures[0].Description)
	}
	if !strings.Contains(out.Failures[1].Description, "Absent from the revision of 2026-07-31") {
		t.Errorf("uncarried breach must say so: %q", out.Failures[1].Description)
	}
	if len(out.Descriptive) != 1 || !strings.Contains(out.Descriptive[0].Title, "predate the first architecture revision (2026-07-15)") {
		t.Fatalf("descriptive = %+v", out.Descriptive)
	}
	if out.Status != StatusRegression {
		t.Fatalf("status = %s", out.Status)
	}
}

// Git's author date grades, and it overrides the history in BOTH directions: a breach
// the revision carried still fails when its witness line is newer than the rule's date,
// and one the revision did not carry still ratchets when the line is older.
func TestApplyTime_WitnessDateGradesRegardlessOfTheRevision(t *testing.T) {
	ruleDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	withWitness := func(title string) facts.Insight {
		in := dated(title, "2026-08-01")
		in.Evidence = append(in.Evidence, facts.Evidence{File: "app/jobs/j.rb", Line: 12})
		return in
	}
	carried := withWitness("Constraint r violated: Carried -> X via calls")
	uncarried := withWitness("Constraint r violated: Uncarried -> X via calls")

	history := func(time.Time) (*facts.Snapshot, string, string, bool) {
		return &facts.Snapshot{Insights: []facts.Insight{{Title: carried.Title}}}, "2026-07-31", "2026-07-15", true
	}

	// The line is NEWER than the rule's date: both grade, including the carried one.
	newer := func(string, int, time.Time) (time.Time, string) { return ruleDate.AddDate(0, 1, 0), "" }
	out := ApplyTime(Verdict{Status: StatusRegression, Failures: []facts.Insight{carried, uncarried}}, nil, history, newer)
	if len(out.Failures) != 2 || len(out.Advisories) != 0 {
		t.Fatalf("a recently changed witness grades even when the revision carried it: failures=%d advisories=%d",
			len(out.Failures), len(out.Advisories))
	}

	// The line is OLDER: both ratchet, including the one the revision did not carry.
	older := func(string, int, time.Time) (time.Time, string) { return ruleDate.AddDate(0, -6, 0), "" }
	out = ApplyTime(Verdict{Status: StatusRegression, Failures: []facts.Insight{carried, uncarried}}, nil, history, older)
	if len(out.Advisories) != 2 || len(out.Failures) != 0 {
		t.Fatalf("an old witness ratchets even when the revision did not carry it: failures=%d advisories=%d",
			len(out.Failures), len(out.Advisories))
	}
	if out.Status != StatusClean {
		t.Errorf("status = %s, want clean", out.Status)
	}
}

// Deleting the history changes no verdict — the rule HISTORY.md states.
func TestApplyTime_HistoryPresenceNeverChangesTheVerdict(t *testing.T) {
	ruleDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	breach := dated("Constraint r violated: A -> B via calls", "2026-08-01")
	breach.Evidence = append(breach.Evidence, facts.Evidence{File: "app/a.rb", Line: 3})
	carrying := func(time.Time) (*facts.Snapshot, string, string, bool) {
		return &facts.Snapshot{Insights: []facts.Insight{{Title: breach.Title}}}, "2026-07-31", "2026-07-15", true
	}
	for _, age := range []WitnessAge{
		func(string, int, time.Time) (time.Time, string) { return ruleDate.AddDate(0, 1, 0), "" },
		func(string, int, time.Time) (time.Time, string) { return ruleDate.AddDate(0, -1, 0), "" },
	} {
		with := ApplyTime(Verdict{Status: StatusRegression, Failures: []facts.Insight{breach}}, nil, carrying, age)
		without := ApplyTime(Verdict{Status: StatusRegression, Failures: []facts.Insight{breach}}, nil, nil, age)
		if with.Status != without.Status || len(with.Failures) != len(without.Failures) {
			t.Errorf("history changed the verdict: with=%s/%d without=%s/%d",
				with.Status, len(with.Failures), without.Status, len(without.Failures))
		}
	}
}

func TestApplyTime_GrowthAllowsTheBaselinePlusAllowance(t *testing.T) {
	within := facts.Insight{Title: "Constraint api-stays-small violated: public-api has 12 members over a cap of 10", Source: "constraints", Evidence: []facts.Evidence{{Fact: "count: 12"}, {Fact: "growth: 2"}}}
	past := facts.Insight{Title: "Constraint api-stays-small violated: public-api has 14 members over a cap of 10", Source: "constraints", Evidence: []facts.Evidence{{Fact: "count: 14"}, {Fact: "growth: 2"}}}
	base := &facts.Snapshot{Insights: []facts.Insight{{Title: "Constraint api-stays-small violated: public-api has 11 members over a cap of 10", Source: "constraints"}}}
	out := ApplyTime(Verdict{Status: StatusRegression, Failures: []facts.Insight{within}}, base, nil, nil)
	if len(out.Failures) != 0 || len(out.Advisories) != 1 || out.Status != StatusClean {
		t.Fatalf("within the allowance is reported, got %+v", out)
	}
	out = ApplyTime(Verdict{Status: StatusRegression, Failures: []facts.Insight{past}}, base, nil, nil)
	if len(out.Failures) != 1 || out.Status != StatusRegression {
		t.Fatalf("past the allowance fails, got %+v", out)
	}
}
