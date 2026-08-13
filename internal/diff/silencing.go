package diff

import (
	"sort"

	"github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/facts"
)

// silencing separates a constraint breach the change FIXED from the two ways it
// can merely stop being reported. All three look identical from the finding
// alone: it was there, it is gone. What tells them apart is what the two
// snapshots declared and what those declarations selected — whether the rule
// still judges the same thing, and whether the code the breach named is still
// measured and still a member of the component the rule binds.
//
// Both readings are resolved by the constraints package itself, from the
// declarations each snapshot carries, so this test can never disagree with the
// explainer about what a rule or a selector means. It is built lazily: a delta
// with no resolved constraint findings pays nothing.
type silencing struct {
	baseline, current *facts.Snapshot
	baseIndex         *constraints.MembershipIndex
	curIndex          *constraints.MembershipIndex
	built             bool
}

func newSilencing(baseline, current *facts.Snapshot) *silencing {
	return &silencing{baseline: baseline, current: current}
}

func (s *silencing) indexes() (*constraints.MembershipIndex, *constraints.MembershipIndex) {
	if !s.built {
		s.built = true
		s.baseIndex = constraints.NewMembershipIndex(snapFacts(s.baseline))
		s.curIndex = constraints.NewMembershipIndex(snapFacts(s.current))
	}
	return s.baseIndex, s.curIndex
}

// byDeclarationChange reports whether a constraint breach present at baseline
// and absent now went quiet because the DECLARATION moved rather than the code:
// the rule was deleted, its form was swapped under a preserved id, or the
// witness was carved out by an exemption. All three leave the breaching code
// byte-identical, and all three printed the breach under "resolved by this
// change" at exit 0 — the exemption twice over, once honestly as excused and
// once falsely as fixed.
//
// The three tests are deliberately narrow, because the failure runs both ways.
// Reading a rule's bookkeeping props — the file it is declared in, the recipe
// that expanded it, the instance label — as terms of the law meant that moving
// a rule between constraints files, or relabelling a recipe instance, filed
// every breach the change genuinely fixed under "the law stopped asking": a
// false factual claim about work someone did. So does comparing the exemption
// SET: adding a carve-out for witness X and fixing witness Y in one change put
// Y there too. The identity excludes all of it (declarationBookkeeping), and
// the exemption arm asks about this witness alone.
//
// It is asked only of violation findings, and that asymmetry is deliberate:
// deleting a broken selector really does resolve "this selector cannot be
// evaluated", where deleting a rule resolves nothing about the code it judged.
func (s *silencing) byDeclarationChange(in facts.Insight) bool {
	if in.Source != constraints.ExplainerName {
		return false
	}
	id := constraints.RuleIDFromTitle(in.Title)
	if id == "" {
		return false
	}
	base, current := s.indexes()
	was, declared := base.Declaration(id)
	if !declared {
		return false
	}
	now, stillDeclared := current.Declaration(id)
	if !stillDeclared || was != now {
		return true
	}
	witness := constraints.WitnessFromTitle(in.Title)
	return witness != "" && current.Exempts(id, witness) && !base.Exempts(id, witness)
}

// byMembershipChange reports whether a constraint breach present at baseline
// and absent now went quiet because its subject left the component. A subject
// the current snapshot no longer measures at all is NOT this case: deleted code
// really did resolve the breach.
func (s *silencing) byMembershipChange(in facts.Insight) bool {
	if in.Source != constraints.ExplainerName || len(in.Evidence) == 0 {
		return false
	}
	id := constraints.RuleIDFromTitle(in.Title)
	if id == "" {
		return false
	}
	base, current := s.indexes()
	for _, component := range base.ComponentsOfRule(id) {
		for _, ev := range in.Evidence {
			for _, name := range []string{ev.Symbol, ev.Fact} {
				if name == "" || !current.Measured(name) {
					continue
				}
				if base.Selects(component, name) && !current.Selects(component, name) {
					return true
				}
			}
		}
	}
	return false
}

// byUnattributable reports whether the snapshot has no standing to say what
// happened to a breach that stopped being reported. Two causes, both of which
// otherwise land in "Resolved by this change" with the breaching code untouched:
//
//   - The witness's REPOSITORY is gone from the current snapshot. A repo dropped
//     from a union append reads exactly like deleted code — the witness is no
//     longer measured — and byMembershipChange skips an unmeasured witness for
//     precisely the reason that it usually IS deleted code. The rule stays
//     declared with an identical identity, so byDeclarationChange is false too,
//     and the still-breaching witness printed as resolved at exit 0. The repo
//     labels each snapshot measured tell the two apart; facts.SameRepo cannot,
//     because it compares the snapshot's own path and a union's members are not
//     that.
//   - The BASELINE declared no such rule. A baseline carrying constraint
//     findings whose declaration it does not carry cannot be compared against at
//     all: neither test can run, and falling through to Resolved credits the
//     change with clearing something the snapshot never established was there.
func (s *silencing) byUnattributable(in facts.Insight) bool {
	if in.Source != constraints.ExplainerName {
		return false
	}
	id := constraints.RuleIDFromTitle(in.Title)
	if id == "" {
		return false
	}
	base, current := s.indexes()
	if _, declared := base.Declaration(id); !declared {
		return true
	}
	measured := current.Repos()
	if len(measured) == 0 {
		return false
	}
	for _, ev := range in.Evidence {
		for _, name := range []string{ev.Symbol, ev.Fact} {
			if name == "" || current.Measured(name) {
				continue
			}
			repo, known := base.RepoOf(name)
			if known && repo != "" && !measured[repo] {
				return true
			}
		}
	}
	return false
}

// droppedRepos names the repository labels the baseline measured and the
// current snapshot does not — a union that lost a member. It is a comparability
// question, not a finding one: every verdict about that repo's code went quiet
// at once, and a reader weighing the delta has to know before reading it.
func droppedRepos(baseline, current *facts.Snapshot) []string {
	base, now := map[string]bool{}, map[string]bool{}
	for _, f := range snapFacts(baseline) {
		if f.Repo != "" {
			base[f.Repo] = true
		}
	}
	for _, f := range snapFacts(current) {
		if f.Repo != "" {
			now[f.Repo] = true
		}
	}
	if len(base) == 0 || len(now) == 0 {
		return nil
	}
	var gone []string
	for repo := range base {
		if !now[repo] {
			gone = append(gone, repo)
		}
	}
	sort.Strings(gone)
	return gone
}
