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

// A breach the revision at the rule's date already carried is reported; one
// it did not carry stays a failure; a date before the first revision keeps
// the failure and says so once.
func TestApplyTime_SinceRatchetsWhatTheRevisionCarried(t *testing.T) {
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
	out := ApplyTime(v, nil, history)
	if len(out.Failures) != 2 || out.Failures[0].Title != fresh.Title || out.Failures[1].Title != early.Title {
		t.Fatalf("failures = %+v", out.Failures)
	}
	if len(out.Advisories) != 1 || out.Advisories[0].Title != old.Title || !strings.Contains(out.Advisories[0].Description, "Present in the revision of 2026-07-31") {
		t.Fatalf("advisories = %+v", out.Advisories)
	}
	if len(out.Descriptive) != 1 || !strings.Contains(out.Descriptive[0].Title, "predate the first architecture revision (2026-07-15)") {
		t.Fatalf("descriptive = %+v", out.Descriptive)
	}
	if out.Status != StatusRegression {
		t.Fatalf("status = %s", out.Status)
	}
}

func TestApplyTime_GrowthAllowsTheBaselinePlusAllowance(t *testing.T) {
	within := facts.Insight{Title: "Constraint api-stays-small violated: public-api has 12 members over a cap of 10", Source: "constraints", Evidence: []facts.Evidence{{Fact: "count: 12"}, {Fact: "growth: 2"}}}
	past := facts.Insight{Title: "Constraint api-stays-small violated: public-api has 14 members over a cap of 10", Source: "constraints", Evidence: []facts.Evidence{{Fact: "count: 14"}, {Fact: "growth: 2"}}}
	base := &facts.Snapshot{Insights: []facts.Insight{{Title: "Constraint api-stays-small violated: public-api has 11 members over a cap of 10", Source: "constraints"}}}
	out := ApplyTime(Verdict{Status: StatusRegression, Failures: []facts.Insight{within}}, base, nil)
	if len(out.Failures) != 0 || len(out.Advisories) != 1 || out.Status != StatusClean {
		t.Fatalf("within the allowance is reported, got %+v", out)
	}
	out = ApplyTime(Verdict{Status: StatusRegression, Failures: []facts.Insight{past}}, base, nil)
	if len(out.Failures) != 1 || out.Status != StatusRegression {
		t.Fatalf("past the allowance fails, got %+v", out)
	}
}
