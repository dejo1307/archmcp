package diff

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// base returns two metas that are comparable in every respect, so a test can vary one
// field and attribute the resulting warning to that field alone.
func comparablePair() (facts.SnapshotMeta, facts.SnapshotMeta) {
	m := facts.SnapshotMeta{
		RepoPath:     "/repo",
		GeneratedAt:  "2026-08-01T10:00:00Z",
		EnolaVersion: "v1",
		Extractors:   []string{"go"},
		Explainers:   []string{"cycles", "layers"},
	}
	cur := m
	cur.GeneratedAt = "2026-08-01T10:00:01Z"
	return m, cur
}

func hasKind(c Comparability, k WarningKind) bool {
	for _, got := range c.Kinds {
		if got == k {
			return true
		}
	}
	return false
}

// An explainer present on one side only contributes its entire finding set as a delta.
// That was recorded in the receipt from the start and never compared, so it was silent.
func TestCompareMeta_ExplainerSetDiffers(t *testing.T) {
	tests := []struct {
		name           string
		baseExplainers []string
		curExplainers  []string
		wantWarn       bool
		wantSubstring  string
	}{
		{
			name:           "identical sets do not warn",
			baseExplainers: []string{"cycles", "layers"},
			curExplainers:  []string{"cycles", "layers"},
			wantWarn:       false,
		},
		{
			name:           "order alone does not warn",
			baseExplainers: []string{"cycles", "layers"},
			curExplainers:  []string{"layers", "cycles"},
			wantWarn:       false,
		},
		{
			name:           "current gained one: its findings all read as NEW",
			baseExplainers: []string{"cycles"},
			curExplainers:  []string{"cycles", "dead-code"},
			wantWarn:       true,
			wantSubstring:  "dead-code",
		},
		{
			name:           "baseline had one the current lost: its findings all read as RESOLVED",
			baseExplainers: []string{"cycles", "dead-code"},
			curExplainers:  []string{"cycles"},
			wantWarn:       true,
			wantSubstring:  "RESOLVED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, c := comparablePair()
			b.Explainers, c.Explainers = tt.baseExplainers, tt.curExplainers

			got := CompareMeta(b, c)

			if hasKind(got, WarnExplainerSet) != tt.wantWarn {
				t.Fatalf("WarnExplainerSet present = %v, want %v (warnings: %v)",
					!tt.wantWarn, tt.wantWarn, got.Warnings)
			}
			if !tt.wantWarn {
				if !got.Comparable {
					t.Errorf("Comparable = false, want true; warnings: %v", got.Warnings)
				}
				return
			}
			if tt.wantSubstring != "" {
				var found bool
				for _, w := range got.Warnings {
					if strings.Contains(w, tt.wantSubstring) {
						found = true
					}
				}
				if !found {
					t.Errorf("no warning contains %q; got %v", tt.wantSubstring, got.Warnings)
				}
			}
		})
	}
}

// The explainer arm must not be reported as an extractor mismatch. They have different
// consequences — a differing extractor set invalidates the fact delta everything else is
// computed from, while a differing explainer set leaves it exact — and pkg/check keys
// blocking-vs-advisory off the kind.
func TestCompareMeta_ExplainerSetIsNotExtractorSet(t *testing.T) {
	b, c := comparablePair()
	b.Explainers, c.Explainers = []string{"cycles"}, []string{"cycles", "hotspots"}

	got := CompareMeta(b, c)

	if hasKind(got, WarnExtractorSet) {
		t.Errorf("explainer difference reported as WarnExtractorSet; kinds = %v", got.Kinds)
	}
	if !hasKind(got, WarnExplainerSet) {
		t.Errorf("WarnExplainerSet missing; kinds = %v", got.Kinds)
	}
}
