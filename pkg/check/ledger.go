package check

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// The law ledger answers a question the gate collects the inputs for and has
// never asked: how much of the declared law is being EXCUSED rather than
// obeyed.
//
// Both excuse mechanisms already demand accountability — a suppression entry
// names an owner, a reason and a date (LoadSuppressions rejects a ledger
// missing any of them), and an `exempt:` carve-out names the same three beside
// the witness it covers. That makes the excuse rate a measurement rather than
// an impression: if most of a rule's breaches are signed away, the rule is
// what is wrong, and nobody can see that from a per-run verdict that reports
// each excuse in isolation.
//
// It is a REPORT, never a gate contribution. Nothing here changes a status or
// an exit code: the number is evidence for a human deciding whether a
// declaration still earns its place, and a gate that failed on its own
// unpopularity would be the last thing anyone left enabled.

// LedgerExcuse is one signed excuse: which mechanism carried it, who signed
// it, and how long it has stood. Owner, reason and date are mandatory in both
// carriers, so an excuse with a blank one is a parse failure upstream rather
// than a hole here.
type LedgerExcuse struct {
	// Kind is "suppression" (the committed ledger) or "exemption" (a carve-out
	// riding the rule itself). Named rather than merged: one silences a finding
	// from outside the law, the other narrows the law, and a reader auditing
	// them is asking different questions.
	Kind    string `json:"kind"`
	Owner   string `json:"owner,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Date    string `json:"date,omitempty"`
	AgeDays int    `json:"age_days,omitempty"`
	// Witness is the exact violation an exemption covers. Empty for a
	// suppression, which selects by rule id or title prefix and may cover many.
	Witness string `json:"witness,omitempty"`
	// Matched reports whether this excuse actually excused anything in this
	// snapshot. An excuse that matches nothing is the loudest thing this report
	// can say: someone signed away a breach that has since been fixed, moved,
	// or stopped being selected, and the signature is still standing.
	Matched bool `json:"matched"`
}

// LedgerRule is one declared rule and what became of it in this snapshot.
type LedgerRule struct {
	ID   string `json:"id"`
	Mode string `json:"mode"`
	// Source is the declaring file — enola-intent.yaml or a file under
	// enola/constraints/ — so a rate points at the file whose owner can act on it.
	Source  string `json:"source,omitempty"`
	Repo    string `json:"repo,omitempty"`
	Because string `json:"because,omitempty"`
	// Breaches counts the violations this rule reported in the current
	// snapshot, suppressed ones included: a suppressed breach is still a breach,
	// which is the whole point of counting them here.
	Breaches int `json:"breaches"`
	// Suppressed is how many of those a ledger entry excused.
	Suppressed int `json:"suppressed"`
	// Exempted counts carve-outs the explainer confirmed applied — breaches
	// that never reached the Breaches count because the law was narrowed.
	Exempted int            `json:"exempted"`
	Excuses  []LedgerExcuse `json:"excuses,omitempty"`
}

// LedgerSummary is the counts alone — what the verdict carries, so a gate's
// JSON grows by a small fixed object rather than by a row per declared rule.
type LedgerSummary struct {
	Rules int `json:"rules"`
	// ByMode counts declared rules per enforcement mode. Advisory rules are
	// reported here rather than folded into the excuse rate: choosing to declare
	// a rule report-only is a statement about the RULE, not an override of a
	// finding, and merging the two would make the headline unreadable.
	ByMode     map[string]int `json:"by_mode,omitempty"`
	Breaches   int            `json:"breaches"`
	Suppressed int            `json:"suppressed"`
	Exempted   int            `json:"exempted"`
	Excused    int            `json:"excused"`
	// OldestExcuseDays is the age of the longest-standing excuse. An excuse
	// nobody has revisited in a year is the signal the reason it names has
	// stopped being true.
	OldestExcuseDays int `json:"oldest_excuse_days,omitempty"`
	// UndatableExcuses counts excuses whose date could not be read. Reported so
	// an unreadable date is never silently the same as a fresh one.
	UndatableExcuses int `json:"undatable_excuses,omitempty"`
	// IdleExcuses counts signed excuses that excused nothing in this snapshot.
	// A law whose excuses have gone idle is not a law being obeyed — it is one
	// carrying signatures nobody has revisited, and that is invisible from any
	// single verdict.
	IdleExcuses int `json:"idle_excuses,omitempty"`
}

// ExcusedShare is excused breaches over every breach the law raised. Zero when
// the law raised nothing — which is not the same as a law nobody excuses, and
// Line says so by staying silent about a share it cannot compute.
func (s LedgerSummary) ExcusedShare() float64 {
	raised := s.Breaches + s.Exempted
	if raised == 0 {
		return 0
	}
	return float64(s.Excused) / float64(raised)
}

// Ledger is the full report: the summary plus a row per declared rule.
type Ledger struct {
	Summary LedgerSummary `json:"summary"`
	Rules   []LedgerRule  `json:"rules,omitempty"`
}

// ComputeLedger reads the declared rules out of the snapshot's own intent
// facts — never by re-parsing the YAML, because the compiled facts are what
// the explainer verdicted and a second reading of the source could disagree
// with them — and joins them to the findings the current snapshot produced.
//
// now is a parameter rather than a call to time.Now so an age is testable and
// a report renders identically for identical inputs.
func ComputeLedger(store *facts.Store, suppressions []Suppression, findings []facts.Insight, now time.Time) Ledger {
	if store == nil {
		return Ledger{}
	}
	byID := map[string]*LedgerRule{}
	var order []string
	for _, f := range store.ByKind(facts.KindIntent) {
		if f.PropString("intent_kind") != "rule" {
			continue
		}
		id := f.PropString("rule")
		if id == "" || byID[id] != nil {
			continue
		}
		rule := &LedgerRule{
			ID:      id,
			Mode:    f.PropString("mode"),
			Source:  f.PropString("source"),
			Repo:    f.Repo,
			Because: f.PropString("because"),
		}
		for _, ex := range intent.DecodeExemptions(f.PropString("exempt")) {
			rule.Excuses = append(rule.Excuses, LedgerExcuse{
				Kind:    "exemption",
				Owner:   ex.Owner,
				Reason:  ex.Because,
				Date:    ex.Since,
				AgeDays: ageDays(ex.Since, now),
				Witness: ex.Witness,
			})
		}
		byID[id] = rule
		order = append(order, id)
	}
	if len(order) == 0 {
		return Ledger{}
	}

	// A suppression selecting a rule id is attributed to that rule. One
	// selecting a title prefix is not attributed at all until it matches a
	// finding, because a prefix is free text and guessing which rule it meant
	// is exactly the kind of inference this vocabulary refuses everywhere else.
	for _, s := range suppressions {
		if s.Rule == "" {
			continue
		}
		if rule := byID[s.Rule]; rule != nil {
			rule.Excuses = append(rule.Excuses, LedgerExcuse{
				Kind:    "suppression",
				Owner:   s.Owner,
				Reason:  s.Reason,
				Date:    s.Date,
				AgeDays: ageDays(s.Date, now),
			})
		}
	}

	// applied records which exemption witnesses the explainer confirmed carved
	// something out, and matchedSuppression which rule-selected ledger entries
	// silenced a finding. Both are read back below to mark an excuse live, so
	// "signed and still doing work" is told apart from "signed and forgotten".
	applied := map[string]map[string]bool{}
	matchedSuppression := map[string]bool{}
	for _, in := range findings {
		if in.Source != constraintsSource {
			continue
		}
		if exemptedFinding(in) {
			rest := strings.TrimPrefix(in.Title, ExemptedTitlePrefix)
			id, witness, found := strings.Cut(rest, ": ")
			if found {
				if rule := byID[id]; rule != nil {
					rule.Exempted++
					if applied[id] == nil {
						applied[id] = map[string]bool{}
					}
					applied[id][witness] = true
				}
			}
			continue
		}
		id := constraints.RuleIDFromTitle(in.Title)
		if id == "" {
			continue
		}
		rule := byID[id]
		if rule == nil {
			continue
		}
		rule.Breaches++
		for _, s := range suppressions {
			if s.suppresses(in) {
				rule.Suppressed++
				if s.Rule != "" {
					matchedSuppression[s.Rule] = true
				}
				break
			}
		}
	}

	led := Ledger{Summary: LedgerSummary{Rules: len(order), ByMode: map[string]int{}}}
	oldest := -1
	for _, id := range order {
		rule := *byID[id]
		sort.SliceStable(rule.Excuses, func(i, j int) bool {
			if rule.Excuses[i].Kind != rule.Excuses[j].Kind {
				return rule.Excuses[i].Kind < rule.Excuses[j].Kind
			}
			return rule.Excuses[i].Witness < rule.Excuses[j].Witness
		})
		for i := range rule.Excuses {
			switch rule.Excuses[i].Kind {
			case "exemption":
				rule.Excuses[i].Matched = applied[rule.ID][rule.Excuses[i].Witness]
			case "suppression":
				rule.Excuses[i].Matched = matchedSuppression[rule.ID]
			}
			if !rule.Excuses[i].Matched {
				led.Summary.IdleExcuses++
			}
		}
		mode := rule.Mode
		if mode == "" {
			mode = "ratchet"
		}
		led.Summary.ByMode[mode]++
		led.Summary.Breaches += rule.Breaches
		led.Summary.Suppressed += rule.Suppressed
		led.Summary.Exempted += rule.Exempted
		for _, ex := range rule.Excuses {
			// An absent date and an unreadable one are the same answer: this
			// excuse cannot be aged. Counted rather than skipped, because an
			// excuse with no age must never be indistinguishable from a fresh one.
			if !datable(ex.Date) {
				led.Summary.UndatableExcuses++
				continue
			}
			if ex.AgeDays > oldest {
				oldest = ex.AgeDays
			}
		}
		led.Rules = append(led.Rules, rule)
	}
	led.Summary.Excused = led.Summary.Suppressed + led.Summary.Exempted
	if oldest >= 0 {
		led.Summary.OldestExcuseDays = oldest
	}
	return led
}

// datable reports whether a date string parses as the YYYY-MM-DD both carriers
// require. LoadSuppressions already enforces it for the ledger; an exemption's
// `since:` reaches here through the compiled fact, so it is checked rather than
// assumed.
func datable(date string) bool {
	_, err := time.Parse("2006-01-02", date)
	return err == nil
}

// ageDays is how long an excuse has stood, floored at zero: a date in the
// future is a typo, and reporting a negative age would read as a measurement
// rather than the mistake it is.
func ageDays(date string, now time.Time) int {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0
	}
	days := int(now.Sub(t).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// AttachLedger carries the law's excuse summary on the verdict. It mirrors
// AttachCensus: pure, computed from the current snapshot and the policy, and
// present on every outcome so a reader can tell a clean run under an unexcused
// law from a clean run under one that has been signed away.
//
// A repository declaring no rules gets no ledger at all. Undeclared is
// unasked — the same counterparty rule every other declared-intent surface
// keeps — and a zeroed ledger would read as a law with nothing wrong with it.
func AttachLedger(v Verdict, store *facts.Store, p Policy, currentFindings []facts.Insight, now time.Time) Verdict {
	led := ComputeLedger(store, p.Suppressions, currentFindings, now)
	if led.Summary.Rules == 0 {
		return v
	}
	summary := led.Summary
	v.Law = &summary
	return v
}

// Line renders the ledger as the one line the verdict prints beside the
// census, or "" when there is no declared law to describe.
func (s *LedgerSummary) Line() string {
	if s == nil || s.Rules == 0 {
		return ""
	}
	// The "law:" prefix already says these are declared, so the count needs no
	// verb; the mode breakdown rides it when there is more than one mode to tell
	// apart.
	parts := []string{plural(s.Rules, "rule", "rules")}
	if modes := s.modeBreakdown(); modes != "" {
		parts[0] += " (" + modes + ")"
	}
	if raised := s.Breaches + s.Exempted; raised == 0 {
		parts = append(parts, "no breaches")
	} else {
		parts = append(parts, plural(raised, "breach", "breaches"))
		if s.Excused == 0 {
			parts = append(parts, "none excused")
		} else {
			parts = append(parts, fmt.Sprintf("%d excused (%.0f%%)", s.Excused, s.ExcusedShare()*100))
			if s.OldestExcuseDays > 0 {
				parts = append(parts, fmt.Sprintf("oldest excuse %s", plural(s.OldestExcuseDays, "day", "days")))
			}
		}
	}
	// Reported whether or not the law raised anything: an undatable excuse
	// standing against a rule with no breaches is the clearest case of one
	// nobody has revisited.
	if s.IdleExcuses > 0 {
		parts = append(parts, plural(s.IdleExcuses, "excuse matched nothing", "excuses matched nothing"))
	}
	if s.UndatableExcuses > 0 {
		parts = append(parts, plural(s.UndatableExcuses, "excuse with an unreadable date", "excuses with an unreadable date"))
	}
	return "law: " + strings.Join(parts, " · ")
}

// modeBreakdown names every mode that has rules, in a fixed order, and omits
// the ones that do not — a repository with only ratchet rules should not have
// to read three zeroes to learn it.
func (s *LedgerSummary) modeBreakdown() string {
	var parts []string
	for _, mode := range []string{"ratchet", "strict", "advisory", "notify"} {
		if n := s.ByMode[mode]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, mode))
		}
	}
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts, ", ")
}
