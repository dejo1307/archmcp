package vocab

import (
	"strings"
	"testing"
)

func ptrInt(v int) *int           { return &v }
func ptrFloat(v float64) *float64 { return &v }

// TestApply_AddsWithoutDiscardingDefaults is the reason list overlays are add/remove
// rather than replace.
//
// A replacing list is a trap: a user adding one framework's base class silently discards
// every default, so the fix for one false edge quietly reintroduces all the others — and
// nothing fails, because a linker with a thinner vocabulary just draws MORE edges. That
// failure is invisible in the output and would only ever be noticed as mysterious extra
// coupling months later.
func TestApply_AddsWithoutDiscardingDefaults(t *testing.T) {
	s, err := Apply(&Overlay{
		FrameworkConventions: ListOverlay{Add: []string{"BaseViewController", "AppDelegate"}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, added := range []string{"BaseViewController", "AppDelegate"} {
		if !s.FrameworkConventions[added] {
			t.Errorf("%q was not added", added)
		}
	}
	// Every default must survive.
	for _, kept := range []string{"ApplicationController", "ApplicationRecord", "Ability"} {
		if !s.FrameworkConventions[kept] {
			t.Errorf("default %q was discarded by an additive overlay", kept)
		}
	}
}

// TestApply_RemoveIsPossible covers the other direction: a default that is wrong for a
// particular estate can be taken out. Removing a generic-path segment makes that path
// LINKABLE again, so this is the one overlay operation that can add edges — which is
// why it must be explicit rather than a side effect of replacing a list.
func TestApply_RemoveIsPossible(t *testing.T) {
	s, err := Apply(&Overlay{
		GenericPathSegments: ListOverlay{Remove: []string{"status"}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if s.GenericPathSegments["status"] {
		t.Error(`"status" should have been removed`)
	}
	if !s.GenericPathSegments["health"] {
		t.Error(`removing "status" must not disturb "health"`)
	}
}

// TestApply_DefaultsAreNotSharedMutableState: Apply must not mutate the built-in set, or
// one snapshot's overlay would leak into the next in a long-running server.
func TestApply_DefaultsAreNotSharedMutableState(t *testing.T) {
	if _, err := Apply(&Overlay{
		FrameworkConventions: ListOverlay{Add: []string{"Leaked"}},
		GenericPathSegments:  ListOverlay{Remove: []string{"health"}},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	fresh := Default()
	if fresh.FrameworkConventions["Leaked"] {
		t.Error("an overlay's addition leaked into Default()")
	}
	if !fresh.GenericPathSegments["health"] {
		t.Error("an overlay's removal leaked into Default()")
	}
}

// TestApply_ThresholdOmittedKeepsDefault is why ThresholdOverlay uses pointers: with a
// plain int, omitting min_shared_symbols would read as 0, and every incidental type-name
// collision between two repos would become a dependency.
func TestApply_ThresholdOmittedKeepsDefault(t *testing.T) {
	s, err := Apply(&Overlay{Thresholds: ThresholdOverlay{MaxSamples: ptrInt(5)}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if s.Thresholds.MaxSamples != 5 {
		t.Errorf("MaxSamples = %d, want 5", s.Thresholds.MaxSamples)
	}
	if got, want := s.Thresholds.MinSharedSymbols, Default().Thresholds.MinSharedSymbols; got != want {
		t.Errorf("an omitted threshold changed: MinSharedSymbols = %d, want the default %d", got, want)
	}
}

// TestApply_RejectsNonsenseThresholds: validation rather than clamping. A zero or
// negative threshold is almost certainly a typo, and silently correcting it would leave
// the user believing a setting took effect that did not — while silently ACCEPTING it
// widens what links, the direction that fabricates edges.
func TestApply_RejectsNonsenseThresholds(t *testing.T) {
	for name, o := range map[string]*Overlay{
		"zero min_shared_symbols":   {Thresholds: ThresholdOverlay{MinSharedSymbols: ptrInt(0)}},
		"negative min_shared_segs":  {Thresholds: ThresholdOverlay{MinSharedSegments: ptrInt(-1)}},
		"similarity above 1":        {Thresholds: ThresholdOverlay{MinFileSimilarity: ptrFloat(1.5)}},
		"vocab share of zero":       {Thresholds: ThresholdOverlay{MaxVocabRepoShare: ptrFloat(0)}},
		"one repo for vocab filter": {Thresholds: ThresholdOverlay{MinReposForVocabFilter: ptrInt(1)}},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Apply(o)
			if err == nil {
				t.Fatalf("accepted a nonsense threshold: %+v", s.Thresholds)
			}
			if s != nil {
				t.Error("a rejected overlay must return no Set — a caller ignoring the error must not get a half-applied vocabulary")
			}
		})
	}
}

// TestApply_ReportsEveryProblem: a config with three mistakes should say so once, not
// three times over three edit-run cycles.
func TestApply_ReportsEveryProblem(t *testing.T) {
	_, err := Apply(&Overlay{Thresholds: ThresholdOverlay{
		MinSharedSymbols:  ptrInt(0),
		MinFileSimilarity: ptrFloat(2),
		MaxSamples:        ptrInt(-4),
	}})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"min_shared_symbols", "min_file_similarity", "max_samples"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

// TestFingerprint_ChangesWithVocabulary is the guard behind the snapshot config hash.
//
// A vocabulary change changes which edges are drawn, so it changes emitted facts. If the
// fingerprint did not move, two snapshots taken under different vocabularies would carry
// the same config hash and compare as equivalent — and diff_snapshot would attribute the
// resulting edge changes to a code change nobody made.
func TestFingerprint_ChangesWithVocabulary(t *testing.T) {
	base := Default().Fingerprint()

	for name, o := range map[string]*Overlay{
		"added word":        {GenericTypeNames: ListOverlay{Add: []string{"widget"}}},
		"removed word":      {GenericPathSegments: ListOverlay{Remove: []string{"health"}}},
		"moved threshold":   {Thresholds: ThresholdOverlay{MinSharedSymbols: ptrInt(4)}},
		"moved float":       {Thresholds: ThresholdOverlay{MinFileSimilarity: ptrFloat(0.75)}},
		"topic separator":   {TopicOwnerSeparator: "-"},
		"suffix added":      {ConventionSuffixes: ListOverlay{Add: []string{"Config"}}},
		"path marker added": {NonContractPaths: ListOverlay{Add: []string{"/vendor/"}}},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Apply(o)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if s.Fingerprint() == base {
				t.Error("fingerprint did not change — two snapshots under different vocabularies would compare as equal")
			}
		})
	}
}

// TestFingerprint_IsStable: the fingerprint feeds a content hash, so it must not vary
// between runs over the same vocabulary. Map iteration order is the obvious hazard.
func TestFingerprint_IsStable(t *testing.T) {
	first := Default().Fingerprint()
	for range 20 {
		if again := Default().Fingerprint(); again != first {
			t.Fatalf("fingerprint is not stable across calls — map ordering is leaking into it:\n%s\n%s", first, again)
		}
	}
	// Order of additions must not matter either.
	a, _ := Apply(&Overlay{GenericTypeNames: ListOverlay{Add: []string{"alpha", "beta"}}})
	b, _ := Apply(&Overlay{GenericTypeNames: ListOverlay{Add: []string{"beta", "alpha"}}})
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("fingerprint depends on the order entries were added")
	}
}
