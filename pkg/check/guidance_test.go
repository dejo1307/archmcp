package check

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/facts"
)

func guidanceIntentStore(extra ...facts.Fact) *facts.Store {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: components", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "component", "component": "components",
				"match": "app/components/**", "source": "wiki/p.md"}},
		facts.Fact{Kind: facts.KindIntent, Name: "rule: getters-cached", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "getters-cached",
				"guide": "components", "mode": "notify",
				"message":   "Expensive derived getters here use @cached — consider it",
				"exemplars": "app/components/sortable-table.js app/components/gone.js",
				"because":   "the recurring perf bug", "source": "wiki/p.md"}},
		facts.Fact{Kind: facts.KindModule, Name: "app/components/sortable-table", File: "app/components/sortable-table.js"},
	)
	store.Add(extra...)
	return store
}

func guidedDelta() *diff.SnapshotDiff {
	return &diff.SnapshotDiff{
		Comparability: diff.Comparability{Comparable: true},
		FactsAdded:    []facts.Fact{{Kind: facts.KindSymbol, Name: "Table", File: "app/components/table.js"}},
	}
}

func unguidedDelta() *diff.SnapshotDiff {
	return &diff.SnapshotDiff{
		Comparability: diff.Comparability{Comparable: true},
		FactsAdded:    []facts.Fact{{Kind: facts.KindSymbol, Name: "Billing", File: "app/services/billing.rb"}},
	}
}

func TestAttachGuidance_RendersOnlyWhenAMatchingFileChanged(t *testing.T) {
	store := guidanceIntentStore()

	v := AttachGuidance(Evaluate(guidedDelta(), Policy{}), store)
	if len(v.Guidance) != 1 || v.Guidance[0].Rule != "getters-cached" {
		t.Fatalf("guidance = %+v, want the components rule matched by the changed file", v.Guidance)
	}
	if got := v.Guidance[0].MatchedFiles; len(got) != 1 || got[0] != "app/components/table.js" {
		t.Errorf("matched files = %+v", got)
	}
	rendered := v.Render()
	if !strings.Contains(rendered, "Guidance for this change (1)") {
		t.Errorf("rendered verdict must carry the guidance section:\n%s", rendered)
	}
	if !strings.Contains(rendered, "because: the recurring perf bug") {
		t.Errorf("rendered guidance must carry its because:\n%s", rendered)
	}
	if !strings.Contains(rendered, "exemplar app/components/sortable-table.js (present)") ||
		!strings.Contains(rendered, "exemplar app/components/gone.js (absent)") {
		t.Errorf("rendered guidance must annotate exemplar presence:\n%s", rendered)
	}

	silent := AttachGuidance(Evaluate(unguidedDelta(), Policy{}), store)
	if len(silent.Guidance) != 0 {
		t.Fatalf("guidance = %+v, want nothing for a delta that touched no guided component", silent.Guidance)
	}
	if strings.Contains(silent.Render(), "Guidance for this change") {
		t.Errorf("the section must not render when nothing matched:\n%s", silent.Render())
	}
}

func TestAttachGuidance_NeverAffectsTheExitCodeInAnyMode(t *testing.T) {
	store := guidanceIntentStore()
	failing := insight("cycles", "Cyclic dependency detected (2 modules)", 1.0)
	cases := map[string]struct {
		d        *diff.SnapshotDiff
		policy   Policy
		wantExit int
	}{
		"clean stays 0": {guidedDelta(), Policy{}, 0},
		"regression stays 1": {&diff.SnapshotDiff{
			Comparability: diff.Comparability{Comparable: true},
			FactsAdded:    guidedDelta().FactsAdded,
			FindingsNew:   []facts.Insight{failing},
		}, Policy{}, 1},
		"warn-only stays 0": {&diff.SnapshotDiff{
			Comparability: diff.Comparability{Comparable: true},
			FactsAdded:    guidedDelta().FactsAdded,
			FindingsNew:   []facts.Insight{failing},
		}, Policy{WarnOnly: true}, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			plain := Evaluate(tc.d, tc.policy)
			v := AttachGuidance(plain, store)
			if len(v.Guidance) != 1 {
				t.Fatalf("guidance = %+v, want it attached in every mode", v.Guidance)
			}
			if v.ExitCode() != tc.wantExit || v.ExitCode() != plain.ExitCode() {
				t.Errorf("exit = %d, want %d and identical to the guidance-free verdict's %d", v.ExitCode(), tc.wantExit, plain.ExitCode())
			}
			if v.Status != plain.Status {
				t.Errorf("status moved from %q to %q: guidance may never grade", plain.Status, v.Status)
			}
		})
	}
}

func TestAttachGuidance_SkipsUngradedVerdicts(t *testing.T) {
	store := guidanceIntentStore()

	declined := guidedDelta()
	declined.AddWarningKind(diff.WarnDifferentRepo, "different repositories")
	if v := AttachGuidance(Evaluate(declined, Policy{}), store); len(v.Guidance) != 0 {
		t.Fatalf("guidance = %+v, want none on a declined verdict: the delta describes production differences, not the change", v.Guidance)
	}

	if v := AttachGuidance(Evaluate(nil, Policy{}), store); len(v.Guidance) != 0 {
		t.Fatalf("guidance = %+v, want none on a usage error", v.Guidance)
	}
}

func TestAttachGuidance_PartialVerdictsCarryGuidanceOverTheGradedDelta(t *testing.T) {
	store := guidanceIntentStore()
	v := Evaluate(guidedDelta(), Policy{})
	v.Status = StatusPartialClean
	v.Intersection = &IntersectionGrading{SharedExtractors: []string{"go"}}
	v = AttachGuidance(v, store)
	if len(v.Guidance) != 1 {
		t.Fatalf("guidance = %+v, want it on a partial verdict over the graded intersection's own delta", v.Guidance)
	}
	if v.ExitCode() != 0 || v.Status != StatusPartialClean {
		t.Errorf("verdict = %q exit %d, guidance must not regrade a partial verdict", v.Status, v.ExitCode())
	}
}

func TestAttachGuidance_NeverLandsInTheExemptedBucket(t *testing.T) {
	v := AttachGuidance(Evaluate(guidedDelta(), Policy{}), guidanceIntentStore())
	if len(v.Exempted) != 0 || len(v.Failures) != 0 || len(v.Advisories) != 0 || len(v.Suppressed) != 0 {
		t.Fatalf("guidance leaked into a graded bucket: exempted %+v failures %+v advisories %+v suppressed %+v",
			v.Exempted, v.Failures, v.Advisories, v.Suppressed)
	}
	if len(v.Guidance) != 1 {
		t.Fatalf("guidance = %+v", v.Guidance)
	}
}

func TestVerdictJSON_GuidanceArrayIsStableAndTriState(t *testing.T) {
	store := guidanceIntentStore(
		facts.Fact{Kind: facts.KindIntent, Name: "rule: actions-named", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "actions-named",
				"guide": "components", "mode": "advisory",
				"message": "actions here are verbs",
				"because": "grep-ability", "source": "wiki/p.md"}},
	)
	d := guidedDelta()
	d.FactsAdded = append(d.FactsAdded, facts.Fact{Kind: facts.KindSymbol, Name: "Avatar", File: "app/components/avatar.js"})

	first, err := AttachGuidance(Evaluate(d, Policy{}), store).JSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := AttachGuidance(Evaluate(d, Policy{}), store).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("two identical evaluations must serialize identically")
	}

	var decoded struct {
		Guidance []constraints.GuidanceMatch `json:"guidance"`
	}
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Guidance) != 2 || decoded.Guidance[0].Rule != "actions-named" || decoded.Guidance[1].Rule != "getters-cached" {
		t.Fatalf("guidance = %+v, want both rules in id order", decoded.Guidance)
	}
	g := decoded.Guidance[1]
	if len(g.MatchedFiles) != 2 || g.MatchedFiles[0] != "app/components/avatar.js" || g.MatchedFiles[1] != "app/components/table.js" {
		t.Errorf("matched files = %+v, want sorted", g.MatchedFiles)
	}
	if len(g.Exemplars) != 2 ||
		g.Exemplars[0].Presence != constraints.PresenceAbsent ||
		g.Exemplars[1].Presence != constraints.PresencePresent {
		t.Errorf("exemplars = %+v, want tri-state presence in the JSON", g.Exemplars)
	}
}

func TestChangedFiles_DerivedFromEverySideOfTheFactDelta(t *testing.T) {
	d := &diff.SnapshotDiff{
		FactsAdded:   []facts.Fact{{Kind: facts.KindSymbol, Name: "A", File: "a.go"}, {Kind: facts.KindModule, Name: "mod"}},
		FactsRemoved: []facts.Fact{{Kind: facts.KindSymbol, Name: "B", File: "b.go"}},
		FactsChanged: []diff.FactChange{{
			Before: facts.Fact{Kind: facts.KindSymbol, Name: "C", File: "old/c.go"},
			After:  facts.Fact{Kind: facts.KindSymbol, Name: "C", File: "new/c.go"},
		}},
	}
	got := changedFiles(d)
	want := map[string]bool{"a.go": true, "b.go": true, "old/c.go": true, "new/c.go": true}
	if len(got) != len(want) {
		t.Fatalf("changedFiles = %+v, want exactly %v", got, want)
	}
	for _, f := range got {
		if !want[f.Path] {
			t.Errorf("unexpected changed file %+v", f)
		}
	}
}
