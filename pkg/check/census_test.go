package check

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

// A run that saw everything prints "could not see: nothing": the zero is an
// answer, not an absence, so the line is on every verdict.
func TestCensus_NothingWhenEveryCauseIsZero(t *testing.T) {
	v := Evaluate(&diff.SnapshotDiff{}, Policy{})
	v = AttachCensus(v, facts.SnapshotMeta{Unseen: &facts.UnseenCensus{}}, Policy{}, nil)
	out := v.Render()
	if !strings.Contains(out, "could not see: nothing\n") {
		t.Fatalf("line missing:\n%s", out)
	}
	if !strings.HasPrefix(out, "PASS") {
		t.Fatalf("headline lost:\n%s", out)
	}
}

// Every cause above zero lands on the line, in order, and the JSON carries the
// same object; an unused ledger entry is counted only here, where the ledger
// is read.
func TestCensus_CausesPrintInOrderAndUnusedSuppressionsAreCounted(t *testing.T) {
	meta := facts.SnapshotMeta{
		Unseen: &facts.UnseenCensus{
			FilesExcludedByIgnore: 3,
			DirsExcludedByIgnore:  1,
			ProviderSkips: []facts.ProviderSkip{
				{Name: "absent", Reason: "command not found: nothing"},
				{Name: "rubydex", Causes: []facts.CensusCause{{Cause: "unresolved constant reference", Count: 12}}},
			},
			OutsideGraph:          map[string]int{facts.RelImports: 7, facts.RelDependsOn: 2},
			DeadExemptions:        1,
			DynamicFeatureClasses: 4,
		},
		Providers: []facts.ProviderRecord{{Name: "rubydex", Overlap: map[string]*facts.RelationOverlap{
			facts.RelImplements: {AlreadyResolved: 10, Conflict: 2},
			facts.RelCalls:      {AlreadyResolved: 5, Respelled: 3},
		}}},
	}
	policy := Policy{Suppressions: []Suppression{
		{FindingTitlePrefix: "Dependency cycle", Owner: "x", Reason: "y", Date: "2026-01-01"},
		{FindingTitlePrefix: "Nothing matches this", Owner: "x", Reason: "y", Date: "2026-01-01"},
	}}
	findings := []facts.Insight{{Title: "Dependency cycle: a -> b", Source: "cycles"}}
	v := Evaluate(&diff.SnapshotDiff{}, policy)
	v = AttachCensus(v, meta, policy, findings)

	line := v.Census.Line()
	want := "could not see: 3 files and 1 directory excluded by ignore globs; absent skipped (command not found: nothing); rubydex: 12 unresolved constant reference; 2 depends_on targets outside the graph; 7 imports targets outside the graph; 1 exemption matching nothing; 1 unused suppression; rubydex: 2 relations contradict the extractor, 3 respelled, 15 repeated; 4 classes carrying a dynamic dispatch"
	if line != want {
		t.Fatalf("line\n got %s\nwant %s", line, want)
	}
	if v.Census.UnusedSuppressions != 1 {
		t.Fatalf("unused suppressions: %d", v.Census.UnusedSuppressions)
	}
	raw, err := v.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Census *Census `json:"census"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Census == nil || decoded.Census.DeadExemptions != 1 || decoded.Census.ProviderOverlap[0].Conflict != 2 {
		t.Fatalf("json census: %+v", decoded.Census)
	}
}

// A snapshot from before the census says so rather than reading as clean.
func TestCensus_SnapshotWithoutCensusIsNamed(t *testing.T) {
	v := Evaluate(&diff.SnapshotDiff{}, Policy{})
	v = AttachCensus(v, facts.SnapshotMeta{}, Policy{}, nil)
	if !strings.Contains(v.Render(), "could not see: not recorded") {
		t.Fatalf("expected the not-recorded line:\n%s", v.Render())
	}
}

// Text and JSON verdicts without a census attached are byte-identical to
// before: the line exists only once AttachCensus ran.
func TestCensus_AbsentUntilAttached(t *testing.T) {
	v := Evaluate(&diff.SnapshotDiff{}, Policy{})
	if strings.Contains(v.Render(), "could not see") {
		t.Fatalf("line printed without a census")
	}
}
