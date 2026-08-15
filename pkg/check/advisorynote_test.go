package check

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

// A finding lands in the advisory list for one of two reasons, and the note under the
// list has to name the right one. The single line this replaced asserted "Confidence <
// 1.00 is a candidate to verify" over every advisory list, including lists whose every
// entry printed `1.00` — a declared-layer violation or an intent set difference, both
// proven by construction and advisory only because their explainer is outside
// --fail-on. Telling a reader a 1.00 finding is uncertain is worse than saying nothing:
// the contradiction sits one line above the number that refutes it.
func TestRender_AdvisoryNoteNamesTheActualReason(t *testing.T) {
	cases := []struct {
		name       string
		advisories []facts.Insight
		policy     Policy
		wantSubstr []string
		notSubstr  []string
	}{
		{
			name:       "proven finding outside --fail-on names the explainer, not confidence",
			advisories: []facts.Insight{insight("layers", "Layer violation: storage -> api", 1.0)},
			policy:     legacyDefault(),
			wantSubstr: []string{"met the 1.00 confidence floor", "outside [cycles]", "--fail-on"},
			notSubstr:  []string{"candidate to verify"},
		},
		{
			name:       "estimate under the floor is a candidate to verify",
			advisories: []facts.Insight{insight("god-class", "God class (400 dependents)", 0.7)},
			policy:     legacyDefault(),
			wantSubstr: []string{"Confidence < 1.00 is a candidate to verify"},
			notSubstr:  []string{"--fail-on"},
		},
		{
			name: "a mixed list says so rather than picking one reason for both",
			advisories: []facts.Insight{
				insight("layers", "Layer violation: storage -> api", 1.0),
				insight("hotspots", "Call-graph hotspot", 0.7),
			},
			policy:     legacyDefault(),
			wantSubstr: []string{"Mixed", "under 1.00 are candidates to verify", "outside [cycles]"},
		},
		{
			// The floor is printed, not hardcoded: --min-confidence=0.5 used to print a
			// sentence about 1.00, which describes a gate the caller did not run.
			name:       "a lowered floor is reported as the floor that ran",
			advisories: []facts.Insight{insight("god-class", "God class (400 dependents)", 0.3)},
			policy:     Policy{FailExplainers: []string{"cycles"}, MinConfidence: 0.5},
			wantSubstr: []string{"Confidence < 0.50 is a candidate to verify"},
			notSubstr:  []string{"1.00"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Evaluate(&diff.SnapshotDiff{
				Comparability: diff.Comparability{Comparable: true},
				FindingsNew:   tc.advisories,
			}, tc.policy)
			if len(v.Advisories) != len(tc.advisories) {
				t.Fatalf("advisories = %d, want %d — the case does not exercise the note",
					len(v.Advisories), len(tc.advisories))
			}

			out := v.Render()
			for _, want := range tc.wantSubstr {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\n--- got ---\n%s", want, out)
				}
			}
			for _, never := range tc.notSubstr {
				if strings.Contains(out, never) {
					t.Errorf("output contains %q, which does not describe why these findings are advisory\n--- got ---\n%s", never, out)
				}
			}
		})
	}
}
