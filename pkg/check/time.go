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
// failure when it did not. Where the history has no revision that old, the
// witness line's git author date decides instead: a line last changed before
// the rule's date is reported, one changed after it is graded, and a line git
// cannot date (no repository, uncommitted, or behind a shallow clone's
// boundary) grades as undated with the cause said once. A cap whose rule
// allows growth fails only when its count exceeds the baseline's count by
// more than the allowance.
func ApplyTime(v Verdict, base *facts.Snapshot, history RevisionAt, age WitnessAge) Verdict {
	var failures []facts.Insight
	noted := map[string]bool{}
	for _, in := range v.Failures {
		if since, ok := sinceOf(in); ok {
			date, err := time.Parse("2006-01-02", since)
			if err == nil {
				// The architecture history ANNOTATES a dated breach; git's author date
				// GRADES it. The two answer different questions — whether the finding
				// was present at the date, versus whether the witness line was last
				// changed before it — and they disagree whenever an old breach's
				// witness was recently renamed, moved or reformatted.
				//
				// Grading with the history made the verdict depend on it, and a history
				// is per-machine unless a shared store is set up and pushed to. The same
				// commit then graded one way on a laptop that held a local record and
				// another in a fresh CI clone that did not. Whatever decides a verdict
				// has to be reproducible from the checkout, which is what blame is and
				// what a local history is not. So the history's better answer is
				// reported, and never subtracted from the failures.
				if history != nil {
					snap, at, first, found := history(date)
					switch {
					case !found:
						if first != "" && !noted["first:"+since] {
							noted["first:"+since] = true
							v.Descriptive = append(v.Descriptive, facts.Insight{
								Title:         fmt.Sprintf("Rules dated %s predate the first architecture revision (%s)", since, first),
								Description:   "A rule holds since a date the history does not reach. Git's author date of each witness line decides whether a breach was there before the date; a line git cannot date grades as introduced after it.",
								Confidence:    1.0,
								Informational: true,
							})
						}
					case carries(snap, in.Title):
						in.Description += fmt.Sprintf(" Present in the revision of %s, before the rule's date — reported, not graded: git's author date of the witness line decides.", at)
					default:
						in.Description += fmt.Sprintf(" Absent from the revision of %s, the newest at or before the rule's date.", at)
					}
				}
				if ratcheted, note := ageDecides(in, date, since, age, &v, noted); ratcheted {
					in.Description += note
					v.Advisories = append(v.Advisories, in)
					continue
				} else if note != "" {
					in.Description += note
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

// ageDecides asks git for the witness line's author date when the history
// store had no answer. True means the breach predates the rule and is
// reported; the note is appended either way, and an unknown cause is also
// recorded once per cause as a descriptive finding.
func ageDecides(in facts.Insight, date time.Time, since string, age WitnessAge, v *Verdict, noted map[string]bool) (bool, string) {
	if age == nil {
		return false, ""
	}
	file, line, ok := witnessOf(in)
	if !ok {
		return false, ""
	}
	at, cause := age(file, line, date)
	if cause == "" {
		if at.Before(date) {
			return true, fmt.Sprintf(" Last changed %s by git's author date, before the rule's date, so it is reported rather than graded.", at.Format("2006-01-02"))
		}
		return false, fmt.Sprintf(" Last changed %s by git's author date, after the rule's date, so it was introduced after the date.", at.Format("2006-01-02"))
	}
	if !noted[cause+":"+since] {
		noted[cause+":"+since] = true
		v.Descriptive = append(v.Descriptive, facts.Insight{
			Title:         fmt.Sprintf("Rules dated %s could not be dated by git: %s", since, ageCauseTitle[cause]),
			Description:   ageCauseDescription[cause],
			Confidence:    1.0,
			Informational: true,
		})
	}
	return false, fmt.Sprintf(" Git could not date the witness line (%s), so the breach grades as undated.", strings.ReplaceAll(cause, "_", " "))
}

var ageCauseTitle = map[string]string{
	AgeNoGit:       "no git history",
	AgeUncommitted: "the witness line is uncommitted",
	AgeShallow:     "the clone is shallow",
}

var ageCauseDescription = map[string]string{
	AgeNoGit:       "The repository is not a git checkout, git is not on PATH, or the witness file is not tracked, so no author date exists to compare with the rule's date. The breach grades as a rule without a date would.",
	AgeUncommitted: "The witness line has no commit yet, so it has no author date. Commit it, or accept that it grades as introduced after the date.",
	AgeShallow:     "The witness line reaches the boundary of a shallow clone whose boundary commit is newer than the rule's date, so its real author date is not in this checkout. Deepen the clone to date it; until then it grades as introduced after the date.",
}

func witnessOf(in facts.Insight) (string, int, bool) {
	for _, e := range in.Evidence {
		if e.File != "" && e.Line > 0 {
			return e.File, e.Line, true
		}
	}
	return "", 0, false
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
