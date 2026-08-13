package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/facts"
)

// SuppressionsFileName is the committed ledger, relative to the repository
// root. It lives under .enola/ beside the other gate artifacts, but unlike
// them it is source: every entry names an owner, a reason and a date, so a
// silenced finding is a decision someone signed, reviewable in the same diff
// as the code it excuses. The gate only ever READS it — writing an excuse is
// exactly the judgment a tool must not automate.
const SuppressionsFileName = ".enola/suppressions.yaml"

// StrictConstraintTitlePrefix marks a strict-mode constraint violation. The
// constraints explainer stamps it (see rule.titled); the gate recognizes it
// and fails such findings from the CURRENT snapshot regardless of baseline
// presence — strict is the one mode that opts out of the ratchet's
// delta-scoping, and the suppression ledger is its only override.
const StrictConstraintTitlePrefix = "Strict constraint"

// constraintsSource is the explainer name constraint findings carry as Source.
const constraintsSource = "constraints"

const ExemptedTitlePrefix = "Exempted from constraint "

// Suppression is one signed excuse: a finding selector plus the accountability
// fields. Exactly one selector — a literal title prefix, or a constraint rule
// id (which matches that rule's violations under every enforcement mode).
type Suppression struct {
	FindingTitlePrefix string `yaml:"finding_title_prefix,omitempty" json:"finding_title_prefix,omitempty"`
	Rule               string `yaml:"rule,omitempty" json:"rule,omitempty"`
	Owner              string `yaml:"owner" json:"owner"`
	Reason             string `yaml:"reason" json:"reason"`
	Date               string `yaml:"date" json:"date"`
}

// suppressionsFile is the ledger's on-disk shape.
type suppressionsFile struct {
	Entries []Suppression `yaml:"entries"`
}

// LoadSuppressions reads the repository's suppression ledger. A missing file
// is an empty ledger — suppressing nothing is the default, not an error. A
// present file parses strictly: unknown fields, a selector-less or
// double-selector entry, or a missing accountability field each reject the
// WHOLE ledger, because a gate that half-reads its excuse list would silence
// findings nobody signed off — or fail ones somebody did.
func LoadSuppressions(repoPath string) ([]Suppression, error) {
	path := filepath.Join(repoPath, filepath.FromSlash(SuppressionsFileName))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", SuppressionsFileName, err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	var file suppressionsFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", SuppressionsFileName, err)
	}
	for i, s := range file.Entries {
		hasPrefix, hasRule := s.FindingTitlePrefix != "", s.Rule != ""
		if hasPrefix == hasRule {
			return nil, fmt.Errorf("%s: entries[%d]: exactly one of finding_title_prefix, rule selects what is suppressed", SuppressionsFileName, i)
		}
		if strings.TrimSpace(s.Owner) == "" {
			return nil, fmt.Errorf("%s: entries[%d]: missing owner — a suppression is a decision someone signs", SuppressionsFileName, i)
		}
		if strings.TrimSpace(s.Reason) == "" {
			return nil, fmt.Errorf("%s: entries[%d]: missing reason", SuppressionsFileName, i)
		}
		if _, err := time.Parse("2006-01-02", s.Date); err != nil {
			return nil, fmt.Errorf("%s: entries[%d]: date %q must be YYYY-MM-DD", SuppressionsFileName, i, s.Date)
		}
	}
	return file.Entries, nil
}

// suppresses reports whether the entry selects this finding. A title prefix is
// a literal prefix match. A rule id matches the constraint-violation titles
// the explainer stamps for that id — under any enforcement mode, because a
// rule flipping ratchet -> strict must not orphan its existing suppression.
func (s Suppression) suppresses(in facts.Insight) bool {
	if s.FindingTitlePrefix != "" {
		return strings.HasPrefix(in.Title, s.FindingTitlePrefix)
	}
	if in.Source != constraintsSource {
		return false
	}
	for _, prefix := range []string{"Constraint", "Advisory constraint", "Strict constraint"} {
		if strings.HasPrefix(in.Title, fmt.Sprintf("%s %s violated:", prefix, s.Rule)) {
			return true
		}
	}
	return false
}

// suppressed reports whether any ledger entry selects the finding.
func (p Policy) suppressed(in facts.Insight) bool {
	for _, s := range p.Suppressions {
		if s.suppresses(in) {
			return true
		}
	}
	return false
}

// strictFinding reports whether a finding is a strict-mode constraint
// violation — the shape the gate fails regardless of baseline presence. Both
// halves are required: the prefix alone could be typed by any explainer, and
// the source alone says nothing about mode.
func strictFinding(in facts.Insight) bool {
	return in.Source == constraintsSource && strings.HasPrefix(in.Title, StrictConstraintTitlePrefix)
}

func exemptedFinding(in facts.Insight) bool {
	return in.Source == constraintsSource && strings.HasPrefix(in.Title, ExemptedTitlePrefix)
}

// withoutExempted drops from the undeclared bucket every breach this delta
// already reports as exempted. An exemption is a declaration change, so the
// bucket is right to catch it — but "excused by name, by an owner, since a
// date" is the more specific and more useful sentence, and printing the same
// witness twice under two headings is how a reader stops reading either.
//
// The join is on the witness the two titles share: a violation is titled
// "<prefix> <rule> violated: <witness>" and its carve-out
// "Exempted from constraint <rule>: <witness>".
func withoutExempted(undeclared, exempted []facts.Insight) []facts.Insight {
	if len(undeclared) == 0 || len(exempted) == 0 {
		return undeclared
	}
	excused := map[string]bool{}
	for _, in := range exempted {
		if rest, cut := strings.CutPrefix(in.Title, ExemptedTitlePrefix); cut {
			excused[rest] = true
		}
	}
	var out []facts.Insight
	for _, in := range undeclared {
		id := constraints.RuleIDFromTitle(in.Title)
		_, witness, found := strings.Cut(in.Title, " violated: ")
		if id != "" && found && excused[id+": "+witness] {
			continue
		}
		out = append(out, in)
	}
	return out
}
