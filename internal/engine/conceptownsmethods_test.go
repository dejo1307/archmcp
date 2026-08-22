package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/intent"
)

// owns: methods reaches the member's METHODS — the has_method edges the graph
// measures — and stops there. What a member lexically encloses otherwise is not
// owned: a constant and a nested class written in its body carry no has_method
// edge, so an edge landing on one lands OUTSIDE the component.
//
// The vocabulary used to be spelled `enclosed`, and its docs promised "the
// facts its members ENCLOSE" — constants, nested classes and attr_accessor
// variables it never delivered. The implementation always read exactly
// RelHasMethod. This fixture is the difference the two words disagree about, so
// the word cannot drift from the measurement again.
const fixtureBudget = `class Budget < StandardError
  LIMIT = 42

  class Overrun
    def self.raise_it
      true
    end
  end

  def self.ceiling
    LIMIT
  end

  def check
    Budget::LIMIT
    Budget::Overrun.raise_it
    Budget.ceiling
  end
end
`

func budgetRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "shop")
	writeFile(t, filepath.Join(repo, "app", "errors", "budget.rb"), fixtureBudget)
	writeFile(t, filepath.Join(repo, "app", "models", "job.rb"), fixtureJob)
	return repo
}

// A member's own method is owned, so a call landing on it stays inside the
// component; a constant and a nested class in the same body are not, so calls
// landing on those are true breaches. One rule, one fixture, both halves.
func TestOwnsMethods_ReachesMethodsAndStopsAtConstantsAndNestedClasses(t *testing.T) {
	insights, err := snapshotInsights(t, budgetRepo(t), declarationFile{
		Components: []intent.ConstraintComponent{
			{Name: "exceptions", Where: map[string]any{"superclass": "StandardError"}, Owns: intent.OwnsMethods},
			{Name: "models", Match: []string{"app/models/**"}, Kind: "symbol"},
		},
		Rules: []intent.ConstraintRule{matrixRule(intent.ConstraintRule{
			Allow: "exceptions", Only: []string{"exceptions"}, Via: "calls"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := unverdictableTitles(insights); len(got) != 0 {
		t.Fatalf("refusals = %v, want none: the concept owns the methods that make these calls", got)
	}
	witnesses := strings.Join(breachWitnesses(insights), "\n")
	// The method call is absorbed: Budget.ceiling is a method of a member, which
	// the declaration says is the member's.
	if strings.Contains(witnesses, "Budget.ceiling") {
		t.Errorf("breaches = %v, want the call landing on a member's own METHOD absorbed by owns: methods", breachWitnesses(insights))
	}
	// The constant and the nested class are not methods, so neither is owned and
	// both land outside the only: component.
	for _, outside := range []string{"Budget::LIMIT", "Budget::Overrun"} {
		if !strings.Contains(witnesses, outside) {
			t.Errorf("breaches = %v, want %s reported: it is not a method, so owns: methods does not reach it", breachWitnesses(insights), outside)
		}
	}
}
