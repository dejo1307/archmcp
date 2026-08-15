package check

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

func cleanDiff() *diff.SnapshotDiff { return &diff.SnapshotDiff{} }

// With no thresholds configured the verdict must be exactly what it was before
// measurements existed. This is the whole basis for the feature being additive: a build
// that passes today has to keep passing after an upgrade, whether or not the caller
// happens to measure things.
func TestMeasurementsWithoutThresholdsChangeNothing(t *testing.T) {
	m := []Measurement{
		{Name: "net_new_orphans", Label: "net-new dead-code orphan(s)", Count: 99},
		{Name: "new_high_perf", Label: "new high-severity performance finding(s)", Count: 42},
	}

	v := Evaluate(cleanDiff(), legacyDefault(), m...)

	if v.Status != StatusClean {
		t.Errorf("Status = %q, want %q — an ungated measurement must not grade", v.Status, StatusClean)
	}
	if v.ExitCode() != 0 {
		t.Errorf("ExitCode = %d, want 0", v.ExitCode())
	}
	if len(v.Breaches) != 0 {
		t.Errorf("Breaches = %v, want none", v.Breaches)
	}
	// Carried even though ungated: a caller that measured something and got silence
	// cannot otherwise tell "under the bound" from "nobody looked".
	if len(v.Measurements) != 2 {
		t.Errorf("Measurements = %d, want 2 — measurements must be reported even when not gated", len(v.Measurements))
	}
}

func TestThresholdGrading(t *testing.T) {
	tests := []struct {
		name       string
		count      int
		threshold  Threshold
		wantStatus Status
		wantBreach bool
		wantFatal  bool
	}{
		{
			name:       "below both bounds is clean and silent",
			count:      1,
			threshold:  Threshold{Measurement: "orphans", WarnAt: 2, FailAt: 4},
			wantStatus: StatusClean,
		},
		{
			name:       "at the warn bound reports without failing",
			count:      2,
			threshold:  Threshold{Measurement: "orphans", WarnAt: 2, FailAt: 4},
			wantStatus: StatusClean,
			wantBreach: true,
		},
		{
			name:       "at the fail bound is a regression",
			count:      4,
			threshold:  Threshold{Measurement: "orphans", WarnAt: 2, FailAt: 4},
			wantStatus: StatusRegression,
			wantBreach: true,
			wantFatal:  true,
		},
		{
			name:       "a zero bound disables that severity",
			count:      50,
			threshold:  Threshold{Measurement: "orphans", FailAt: 0, WarnAt: 0},
			wantStatus: StatusClean,
		},
		{
			name:       "fail-only threshold still fails",
			count:      3,
			threshold:  Threshold{Measurement: "orphans", FailAt: 3},
			wantStatus: StatusRegression,
			wantBreach: true,
			wantFatal:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Evaluate(cleanDiff(), Policy{Thresholds: []Threshold{tt.threshold}},
				Measurement{Name: "orphans", Label: "orphan(s)", Count: tt.count})

			if v.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", v.Status, tt.wantStatus)
			}
			if got := len(v.Breaches) > 0; got != tt.wantBreach {
				t.Errorf("breach reported = %v, want %v", got, tt.wantBreach)
			}
			if tt.wantBreach && v.Breaches[0].Fatal != tt.wantFatal {
				t.Errorf("Fatal = %v, want %v", v.Breaches[0].Fatal, tt.wantFatal)
			}
		})
	}
}

// A threshold naming a measurement nobody reported must not fire. Otherwise a policy
// would grade on the ABSENCE of a signal, and a caller that simply cannot measure
// something would fail every build.
func TestThresholdWithoutItsMeasurementIsInert(t *testing.T) {
	v := Evaluate(cleanDiff(), Policy{Thresholds: []Threshold{{Measurement: "never_reported", FailAt: 1}}})

	if v.Status != StatusClean {
		t.Errorf("Status = %q, want %q", v.Status, StatusClean)
	}
	if len(v.Breaches) != 0 {
		t.Errorf("Breaches = %v, want none", v.Breaches)
	}
}

// --warn-only must suppress a fatal breach exactly as it suppresses a failing finding.
func TestWarnOnlySuppressesFatalBreach(t *testing.T) {
	v := Evaluate(cleanDiff(),
		Policy{WarnOnly: true, Thresholds: []Threshold{{Measurement: "orphans", FailAt: 1}}},
		Measurement{Name: "orphans", Label: "orphan(s)", Count: 5})

	if v.Status != StatusClean {
		t.Errorf("Status = %q, want %q under --warn-only", v.Status, StatusClean)
	}
	if len(v.Breaches) != 1 || !v.Breaches[0].Fatal {
		t.Errorf("the breach must still be REPORTED under --warn-only; got %v", v.Breaches)
	}
}

// The headline counts breaches, not only findings. A change failing solely on a
// measurement would otherwise read "FAIL — 0 structural regressions introduced".
func TestRenderCountsBreachesInTheHeadline(t *testing.T) {
	out := Evaluate(cleanDiff(),
		Policy{Thresholds: []Threshold{{Measurement: "orphans", FailAt: 1}}},
		Measurement{Name: "orphans", Label: "net-new dead-code orphan(s)", Count: 3}).Render()

	if strings.Contains(out, "0 structural regressions") {
		t.Errorf("headline contradicts the verdict:\n%s", out)
	}
	if !strings.Contains(out, "FAIL — 1 structural regression introduced") {
		t.Errorf("headline does not count the breach:\n%s", out)
	}
	if !strings.Contains(out, "net-new dead-code orphan(s)") {
		t.Errorf("breach not reported in the text verdict:\n%s", out)
	}
}

// Findings and breaches are counted together, so a change tripping both reads as one
// total rather than two competing numbers.
func TestRenderCombinesFindingsAndBreaches(t *testing.T) {
	d := &diff.SnapshotDiff{FindingsNew: []facts.Insight{
		{Source: "cycles", Title: "Cyclic dependency detected (2 modules)", Confidence: 1.0},
	}}

	out := Evaluate(d, Policy{FailExplainers: []string{"cycles"}, Thresholds: []Threshold{{Measurement: "orphans", FailAt: 1}}},
		Measurement{Name: "orphans", Label: "orphan(s)", Count: 2}).Render()

	if !strings.Contains(out, "FAIL — 2 structural regressions introduced") {
		t.Errorf("headline does not combine the failing finding and the breach:\n%s", out)
	}
}
