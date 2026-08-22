package check

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/enola-labs/enola/internal/facts"
)

// RevisionAt answers the snapshot the architecture history recorded at or
// before a date for the graded repository, with the revision's own date, or
// false when no revision that old exists. The first revision's date is
// returned either way so a rule dated before it can say so.
type RevisionAt func(date time.Time) (snapshot *facts.Snapshot, at string, first string, ok bool)

// ApplyTime grades the dated and growth-bounded constraint verdicts. A
// breach whose rule holds since a date is ratcheted to a report when the
// history revision at or before that date already carried it, and stays a
// failure when it did not; a rule dated before the first revision keeps its
// failures and gains a descriptive finding naming that first date. A cap
// whose rule allows growth fails only when its count exceeds the baseline's
// count by more than the allowance.
func ApplyTime(v Verdict, base *facts.Snapshot, history RevisionAt) Verdict {
	var failures []facts.Insight
	predated := map[string]bool{}
	for _, in := range v.Failures {
		if since, ok := sinceOf(in); ok && history != nil {
			date, err := time.Parse("2006-01-02", since)
			if err == nil {
				snap, at, first, found := history(date)
				switch {
				case !found:
					if first != "" && !predated[since] {
						predated[since] = true
						v.Descriptive = append(v.Descriptive, facts.Insight{
							Title:         fmt.Sprintf("Rules dated %s predate the first architecture revision (%s)", since, first),
							Description:   "A rule holds since a date the history does not reach, so every breach of it grades as introduced after the date. Pin a revision before the date, or date the rule at the first revision, to ratchet what was already there.",
							Confidence:    1.0,
							Informational: true,
						})
					}
				case carries(snap, in.Title):
					in.Description += fmt.Sprintf(" Present in the revision of %s, before the rule's date, so it is reported rather than graded.", at)
					v.Advisories = append(v.Advisories, in)
					continue
				default:
					in.Description += fmt.Sprintf(" Absent from the revision of %s, the newest at or before the rule's date, so it was introduced after the date.", at)
				}
			}
		}
		if allowance, ok := growthOf(in); ok && base != nil {
			count, hasCount := countOf(in)
			baseCount, hasBase := baselineCount(base, in)
			if hasCount && hasBase && count <= baseCount+allowance {
				in.Description += fmt.Sprintf(" The count is %d against %d in the baseline, within the allowed growth of %d, so it is reported rather than graded.", count, baseCount, allowance)
				v.Advisories = append(v.Advisories, in)
				continue
			}
		}
		failures = append(failures, in)
	}
	v.Failures = failures
	if len(v.Failures) == 0 {
		switch v.Status {
		case StatusRegression:
			v.Status = StatusClean
		case StatusPartialRegression:
			v.Status = StatusPartialClean
		}
	}
	return v
}

func sinceOf(in facts.Insight) (string, bool) {
	for _, e := range in.Evidence {
		if strings.HasPrefix(e.Fact, "since: ") {
			return strings.TrimPrefix(e.Fact, "since: "), true
		}
	}
	return "", false
}

func growthOf(in facts.Insight) (int, bool) {
	for _, e := range in.Evidence {
		if strings.HasPrefix(e.Fact, "growth: ") {
			n, err := strconv.Atoi(strings.TrimPrefix(e.Fact, "growth: "))
			return n, err == nil
		}
	}
	return 0, false
}

func countOf(in facts.Insight) (int, bool) {
	for _, e := range in.Evidence {
		if strings.HasPrefix(e.Fact, "count: ") {
			n, err := strconv.Atoi(strings.TrimPrefix(e.Fact, "count: "))
			return n, err == nil
		}
	}
	if m := capCount.FindStringSubmatch(in.Title); m != nil {
		n, err := strconv.Atoi(m[1])
		return n, err == nil
	}
	return 0, false
}

var capCount = regexp.MustCompile(` has (\d+) members over a cap of `)

// baselineCount finds the same cap rule's verdict in the baseline by the
// title's rule prefix and reads its count.
func baselineCount(base *facts.Snapshot, in facts.Insight) (int, bool) {
	prefix := in.Title
	if i := strings.Index(prefix, " has "); i > 0 {
		prefix = prefix[:i]
	}
	for _, b := range base.Insights {
		if b.Source != in.Source || !strings.HasPrefix(b.Title, prefix) {
			continue
		}
		if n, ok := countOf(b); ok {
			return n, true
		}
	}
	return 0, false
}

func carries(snap *facts.Snapshot, title string) bool {
	if snap == nil {
		return false
	}
	for _, in := range snap.Insights {
		if in.Title == title {
			return true
		}
	}
	return false
}
