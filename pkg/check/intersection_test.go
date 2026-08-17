package check

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/extractors/mdintent"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

func intersectionMeta(extractorNames []string, provs []facts.ProviderRecord, generatedAt string) facts.SnapshotMeta {
	return facts.SnapshotMeta{
		RepoPath:         "/repo/app",
		GeneratedAt:      generatedAt,
		Extractors:       extractorNames,
		Explainers:       []string{"constraints", "cycles", "intentcheck"},
		EnolaVersion:     "0.3.15",
		ExtractorVersion: "sha256:extract-1",
		IgnoreGlobHash:   "sha256:globs-1",
		Providers:        provs,
	}
}

func goModuleFact(name, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindModule, Name: name, File: file}
}

func mdIntentFact(pageFile string) facts.Fact {
	return facts.Fact{
		Kind:  facts.KindIntent,
		Name:  "page: " + pageFile,
		File:  pageFile,
		Props: map[string]any{"intent_kind": "page", "source": pageFile},
	}
}

func rbsProviderFact(name string) facts.Fact {
	return facts.Fact{
		Kind:  facts.KindSymbol,
		Name:  name,
		File:  "sig/" + name + ".rbs",
		Props: map[string]any{"provider": "rbs"},
	}
}

func testOwnership() FileOwnership {
	return FileOwnership{
		"go":       func(rel string) bool { return strings.HasSuffix(rel, ".go") },
		"mdintent": func(rel string) bool { return strings.HasSuffix(rel, ".md") },
	}
}

func regrade(t *testing.T, base, current *facts.Snapshot, p Policy, owners FileOwnership) Verdict {
	t.Helper()
	declined := EvaluateCurrent(diff.Compute(base, current), p, current.Insights)
	return RegradeIntersection(declined, base, current, p, owners, current.Insights, "")
}

func mdintentPair(extraCurrentInsights ...facts.Insight) (*facts.Snapshot, *facts.Snapshot) {
	sharedFacts := []facts.Fact{
		goModuleFact("app/pkg/a", "pkg/a/a.go"),
		goModuleFact("app/pkg/b", "pkg/b/b.go"),
	}
	base := &facts.Snapshot{
		Meta:  intersectionMeta([]string{"go"}, nil, "2026-08-10T10:00:00Z"),
		Facts: sharedFacts,
	}
	current := &facts.Snapshot{
		Meta: intersectionMeta([]string{"go", "mdintent"}, nil, "2026-08-10T11:00:00Z"),
		Facts: append(append([]facts.Fact{}, sharedFacts...),
			mdIntentFact("wiki/decisions/api.md"),
			mdIntentFact("wiki/decisions/storage.md"),
		),
		Insights: append([]facts.Insight{{
			Source:     "intentcheck",
			Title:      "Intent declared: wiki/decisions/api.md",
			Confidence: 1.0,
			Evidence:   []facts.Evidence{{File: "wiki/decisions/api.md"}},
		}}, extraCurrentInsights...),
	}
	return base, current
}

func TestRegradeIntersection_BaselineLacksExtractorGradesSharedSet(t *testing.T) {
	base, current := mdintentPair()
	v := regrade(t, base, current, legacyDefault(), testOwnership())

	if v.Status != StatusPartialClean {
		t.Fatalf("status = %q, want %q", v.Status, StatusPartialClean)
	}
	if v.ExitCode() != 0 {
		t.Errorf("exit = %d, want 0", v.ExitCode())
	}
	if v.FactsAdded != 0 || v.FactsRemoved != 0 {
		t.Errorf("facts added/removed = %d/%d, want 0/0 after excluding mdintent facts", v.FactsAdded, v.FactsRemoved)
	}
	if v.Intersection == nil {
		t.Fatal("intersection grading missing from a partial verdict")
	}
	if got := v.Intersection.SharedExtractors; len(got) != 1 || got[0] != "go" {
		t.Errorf("shared extractors = %v, want [go]", got)
	}
	if len(v.Intersection.Excluded) != 1 {
		t.Fatalf("excluded producers = %v, want exactly mdintent", v.Intersection.Excluded)
	}
	ex := v.Intersection.Excluded[0]
	if ex.Name != "mdintent" || ex.Kind != ProducerExtractor || ex.LackedBy != SideBaseline {
		t.Errorf("excluded producer = %+v, want mdintent extractor lacked by baseline", ex)
	}
	if ex.CurrentFactsExcluded != 2 || ex.CurrentFindingsExcluded != 1 {
		t.Errorf("current-side exclusions = %d facts, %d findings, want 2 and 1", ex.CurrentFactsExcluded, ex.CurrentFindingsExcluded)
	}

	out := v.Render()
	for _, want := range []string{
		"PASS (partial verdict)",
		"NOT a full verdict",
		"Graded over the shared producer set (1 family: go)",
		"mdintent (baseline lacks it)",
		"2 facts and 1 finding on the current side not graded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
}

func TestRegradeIntersection_SharedFamilyRegressionStillFails(t *testing.T) {
	base, current := mdintentPair(facts.Insight{
		Source:     "cycles",
		Title:      "Cyclic dependency detected (2 modules)",
		Confidence: 1.0,
		Evidence:   []facts.Evidence{{Fact: "app/pkg/a"}, {Fact: "app/pkg/c"}},
	})
	current.Facts = append(current.Facts, goModuleFact("app/pkg/c", "pkg/c/c.go"))

	v := regrade(t, base, current, legacyDefault(), testOwnership())

	if v.Status != StatusPartialRegression {
		t.Fatalf("status = %q, want %q", v.Status, StatusPartialRegression)
	}
	if v.ExitCode() != 1 {
		t.Errorf("exit = %d, want 1", v.ExitCode())
	}
	if len(v.Failures) != 1 || !strings.Contains(v.Failures[0].Title, "Cyclic dependency") {
		t.Errorf("failures = %+v, want the shared-family cycle", v.Failures)
	}
	out := v.Render()
	if !strings.Contains(out, "FAIL (partial verdict)") {
		t.Errorf("render lacks the partial FAIL headline:\n%s", out)
	}
	if !strings.Contains(out, "mdintent (baseline lacks it)") {
		t.Errorf("render lacks the exclusion note:\n%s", out)
	}
}

func TestRegradeIntersection_ExcludedFamilyRegressionIsNotGradedAndSaysSo(t *testing.T) {
	base, current := mdintentPair(
		facts.Insight{
			Source:     "constraints",
			Title:      "Constraint intent-coverage violated: wiki/decisions/api.md must anchor a module",
			Confidence: 1.0,
			Evidence:   []facts.Evidence{{File: "wiki/decisions/api.md"}},
		},
		facts.Insight{
			Source:     "constraints",
			Title:      "Strict constraint intent-owner violated: wiki/decisions/storage.md names no owner",
			Confidence: 1.0,
			Evidence:   []facts.Evidence{{File: "wiki/decisions/storage.md"}},
		},
	)

	v := regrade(t, base, current, legacyDefault(), testOwnership())

	if v.Status != StatusPartialClean {
		t.Fatalf("status = %q, want %q: an excluded family's violations cannot be graded", v.Status, StatusPartialClean)
	}
	if len(v.Failures) != 0 || len(v.Advisories) != 0 {
		t.Errorf("failures = %+v, advisories = %+v, want none — the findings cite excluded facts", v.Failures, v.Advisories)
	}
	if got := v.Intersection.Excluded[0].CurrentFindingsExcluded; got != 3 {
		t.Errorf("current findings excluded = %d, want 3", got)
	}
	out := v.Render()
	if !strings.Contains(out, "cannot be graded here and is NOT reported") {
		t.Errorf("render does not say excluded regressions are invisible:\n%s", out)
	}
}

func TestRegradeIntersection_CurrentLacksProviderGradesSharedSet(t *testing.T) {
	sharedFacts := []facts.Fact{goModuleFact("app/pkg/a", "pkg/a/a.go")}
	base := &facts.Snapshot{
		Meta: intersectionMeta([]string{"go"},
			[]facts.ProviderRecord{{Name: "rbs", Version: "3.4", FactCount: 2}}, "2026-08-10T10:00:00Z"),
		Facts: append(append([]facts.Fact{}, sharedFacts...),
			rbsProviderFact("App::User"), rbsProviderFact("App::Account")),
	}
	current := &facts.Snapshot{
		Meta: intersectionMeta([]string{"go"},
			[]facts.ProviderRecord{{Name: "rbs", Skipped: true, Reason: "tool not installed"}}, "2026-08-10T11:00:00Z"),
		Facts: sharedFacts,
	}

	v := regrade(t, base, current, legacyDefault(), testOwnership())

	if v.Status != StatusPartialClean {
		t.Fatalf("status = %q, want %q", v.Status, StatusPartialClean)
	}
	if v.FactsRemoved != 0 {
		t.Errorf("facts removed = %d, want 0 after excluding the provider's facts", v.FactsRemoved)
	}
	ex := v.Intersection.Excluded[0]
	if ex.Name != "rbs" || ex.Kind != ProducerProvider || ex.LackedBy != SideCurrent {
		t.Errorf("excluded producer = %+v, want rbs provider lacked by current", ex)
	}
	if ex.BaselineFactsExcluded != 2 {
		t.Errorf("baseline facts excluded = %d, want 2", ex.BaselineFactsExcluded)
	}
	out := v.Render()
	if !strings.Contains(out, "rbs provider (current lacks it)") {
		t.Errorf("render lacks the provider exclusion by name:\n%s", out)
	}
}

func TestRegradeIntersection_VersionMismatchStillDeclines(t *testing.T) {
	base, current := mdintentPair()
	current.Meta.ExtractorVersion = "sha256:extract-2"

	v := regrade(t, base, current, legacyDefault(), testOwnership())

	if v.Status != StatusIncomparable {
		t.Fatalf("status = %q, want %q: identity-corrupting mismatches must keep the hard decline", v.Status, StatusIncomparable)
	}
	if v.ExitCode() != 3 {
		t.Errorf("exit = %d, want 3", v.ExitCode())
	}
	if v.Intersection != nil {
		t.Error("a declined verdict must not carry intersection grading")
	}
}

func TestRegradeIntersection_UnattributableExtractorKeepsDecline(t *testing.T) {
	base, current := mdintentPair()
	owners := testOwnership()
	delete(owners, "mdintent")

	v := regrade(t, base, current, legacyDefault(), owners)

	if v.Status != StatusIncomparable {
		t.Fatalf("status = %q, want %q when the disputed extractor cannot be attributed", v.Status, StatusIncomparable)
	}
	found := false
	for _, w := range v.ComparabilityWarnings {
		if strings.Contains(w, "mdintent") && strings.Contains(w, "cannot be attributed") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings do not say why intersection grading was unavailable: %v", v.ComparabilityWarnings)
	}
}

func TestRegradeIntersection_DifferentRepoIsUntouched(t *testing.T) {
	base, current := mdintentPair()
	base.Meta.RepoPath = "/repo/other"

	v := regrade(t, base, current, legacyDefault(), testOwnership())

	if v.Status != StatusIncomparable || v.Intersection != nil {
		t.Fatalf("status = %q intersection = %v, want the unchanged decline", v.Status, v.Intersection)
	}
}

func TestRegradeIntersection_WarnOnlyKeepsPartialHeadline(t *testing.T) {
	base, current := mdintentPair(facts.Insight{
		Source:     "cycles",
		Title:      "Cyclic dependency detected (2 modules)",
		Confidence: 1.0,
		Evidence:   []facts.Evidence{{Fact: "app/pkg/a"}, {Fact: "app/pkg/c"}},
	})
	current.Facts = append(current.Facts, goModuleFact("app/pkg/c", "pkg/c/c.go"))

	v := regrade(t, base, current, Policy{FailExplainers: []string{"cycles"}, WarnOnly: true}, testOwnership())

	if v.Status != StatusPartialClean {
		t.Fatalf("status = %q, want %q", v.Status, StatusPartialClean)
	}
	if v.ExitCode() != 0 {
		t.Errorf("exit = %d, want 0", v.ExitCode())
	}
	if !strings.Contains(v.Render(), "PASS (partial verdict, --warn-only)") {
		t.Errorf("render lacks the combined partial + warn-only headline:\n%s", v.Render())
	}
}

func TestRegradeIntersection_JSONNamesThePartialVerdict(t *testing.T) {
	base, current := mdintentPair()
	v := regrade(t, base, current, legacyDefault(), testOwnership())

	raw, err := v.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Status       string               `json:"status"`
		Intersection *IntersectionGrading `json:"intersection_grading"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "partial_clean" {
		t.Errorf("json status = %q, want partial_clean", doc.Status)
	}
	if doc.Intersection == nil || len(doc.Intersection.Excluded) != 1 || doc.Intersection.Excluded[0].Name != "mdintent" {
		t.Errorf("json intersection grading = %+v, want mdintent excluded", doc.Intersection)
	}
}

func TestRegradeIntersection_OutputIsDeterministic(t *testing.T) {
	base, current := mdintentPair()
	first := regrade(t, base, current, legacyDefault(), testOwnership())
	second := regrade(t, base, current, legacyDefault(), testOwnership())

	if first.Render() != second.Render() {
		t.Error("render differs across identical regrades")
	}
	j1, err1 := first.JSON()
	j2, err2 := second.JSON()
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !bytes.Equal(j1, j2) {
		t.Error("JSON differs across identical regrades")
	}
}

func TestOwnershipFromExtractors_AttributesByOwnership(t *testing.T) {
	owners := OwnershipFromExtractors([]plugin.Extractor{mdintent.New(), fakeFileOwnerExtractor{}})

	md, ok := owners["mdintent"]
	if !ok {
		t.Fatal("mdintent missing from ownership")
	}
	if !md("wiki/decisions/api.md") || md("app/models/user.rb") {
		t.Error("mdintent ownership must claim exactly markdown files")
	}
	fake, ok := owners["fakelang"]
	if !ok {
		t.Fatal("FileOwner extractor missing from ownership")
	}
	if !fake("main.fake") {
		t.Error("FileOwner ownership not wired through OwnsFile")
	}
	if _, ok := owners["nobody"]; ok {
		t.Error("an extractor with no ownership claim must be absent")
	}
}

type fakeFileOwnerExtractor struct{}

func (fakeFileOwnerExtractor) Name() string                { return "fakelang" }
func (fakeFileOwnerExtractor) Detect(string) (bool, error) { return false, nil }
func (fakeFileOwnerExtractor) OwnsFile(relFile string) bool {
	return strings.HasSuffix(relFile, ".fake")
}
func (fakeFileOwnerExtractor) Extract(_ context.Context, _ string, _ []string) ([]facts.Fact, error) {
	return nil, nil
}
