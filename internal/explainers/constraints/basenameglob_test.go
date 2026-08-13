package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// A convention can live in a filename and in several trees at once. Stimulus
// binds a controller by the `_controller.js` suffix, and a Rails monolith that
// also renders view components keeps them in two places by design — so the
// concept "every Stimulus controller" has no prefix, and before the basename
// form it could not be declared at all.
func TestMatchConstraintPath_BasenameGlobCrossesTrees(t *testing.T) {
	pattern := []string{"**/*_controller.js"}
	for _, path := range []string{
		"app/javascript/controllers/search_controller.js",
		"app/components/nav/nav_controller.js",
		"root_controller.js",
	} {
		if !matchConstraintPath(path, pattern) {
			t.Errorf("%q must match %q — the suffix is the convention, wherever the file sits", path, pattern[0])
		}
	}
	for _, path := range []string{
		"app/javascript/controllers/search_controller.js.map",
		"app/javascript/controllers/search_controller.ts",
		"app/javascript/x_controller.js/nested.js",
		"app/javascript/controllers/controller.js",
	} {
		if matchConstraintPath(path, pattern) {
			t.Errorf("%q must not match %q — the glob applies to the whole final segment, and the literal must be there", path, pattern[0])
		}
	}
}

func TestMatchConstraintPath_BasenameFormsAndTheirBoundary(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"**/Gemfile", "Gemfile", true},
		{"**/Gemfile", "vendor/gems/Gemfile", true},
		{"**/Gemfile", "vendor/gems/Gemfile.lock", false},
		{"**/schema.*", "db/schema.rb", true},
		{"**/schema.*", "db/schema", false},
		{"**/a*a", "lib/a", false},
		{"**/a*a", "lib/aa", true},
		{"**/a*a", "lib/aba", true},

		// Malformed under the grammar, so the evaluator matches nothing rather
		// than inventing a reading the validator never admitted.
		{"**/*", "anything.rb", false},
		{"**/*_a*.rb", "lib/x_ab.rb", false},
		{"**/[a-z].rb", "lib/a.rb", false},
		{"**/controllers/*.js", "app/controllers/x.js", false},
		{"**/", "lib/x.rb", false},

		// The forms that existed before, at the boundary the basename branch
		// must not have moved.
		{"app/domain/**", "app/domain", true},
		{"app/domain/**", "app/domain/billing.rb", true},
		{"app/domain/**", "app/domain2/billing.rb", false},
		{"app/lib/event_bus.rb", "app/lib/event_bus.rb", true},
		{"app/lib/event_bus.rb", "app/lib/event_bus.rb.orig", false},
		{"**", "app/domain/billing.rb", false},
	} {
		if got := matchConstraintPath(tc.path, []string{tc.pattern}); got != tc.want {
			t.Errorf("match(%q, %q) = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
}

// A basename glob that is well formed and selects nothing is the same dead
// selector any other empty component is — the 0.4 advisory, not silence and
// not the declaration-time error a malformed glob gets. The two failures read
// differently because they are different: one is a typo in the grammar, the
// other is a convention that moved or never existed.
func TestExplain_WellFormedBasenameGlobSelectingNothingIsTheDeadSelectorAdvisory(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("stimulus", "**/*_nonexistent.js"),
		facts.Fact{Kind: facts.KindSymbol, Name: "SearchController", File: "app/javascript/controllers/search_controller.js"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %+v, want exactly the dead-selector advisory", insights)
	}
	if !strings.Contains(insights[0].Description, "selects no measured fact") {
		t.Errorf("description = %q, want the dead-selector advisory", insights[0].Description)
	}
	if insights[0].Confidence != emptyComponentConfidence {
		t.Errorf("confidence = %v, want %v — the advisory an empty path component already gets", insights[0].Confidence, emptyComponentConfidence)
	}
}

// The end the capability exists for: a rule over a component no prefix can
// select, verdicting on members from both trees and on neither of the files
// that merely sit beside them.
func TestExplain_BasenameGlobComponentIsRuleEnforceable(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("stimulus", "**/*_controller.js"),
		facts.Fact{Kind: facts.KindIntent, Name: "rule: controllers-are-not-global", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "controllers-are-not-global",
				"forbid_fact": "stimulus", "because": "a Stimulus controller is bound by its element, never by a global",
				"source": "wiki/p.md"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "SearchController", File: "app/javascript/controllers/search_controller.js"},
		facts.Fact{Kind: facts.KindSymbol, Name: "NavController", File: "app/components/nav/nav_controller.js"},
		facts.Fact{Kind: facts.KindSymbol, Name: "helpers", File: "app/javascript/controllers/helpers.js"},
		facts.Fact{Kind: facts.KindSymbol, Name: "NavComponent", File: "app/components/nav/nav_component.rb"},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, insight := range insights {
		if !strings.Contains(insight.Title, "controllers-are-not-global violated") {
			continue
		}
		for _, e := range insight.Evidence {
			files = append(files, e.File)
		}
	}
	if len(files) != 2 {
		t.Fatalf("evidenced files = %v, want the two controllers and nothing else: %+v", files, insights)
	}
	if files[0] != "app/components/nav/nav_controller.js" || files[1] != "app/javascript/controllers/search_controller.js" {
		t.Errorf("evidenced files = %v, want one member from each tree", files)
	}
}
