package facts

import (
	"encoding/json"
	"strings"
	"testing"
)

// The metric accessors exist for one hazard: a map survives the JSON round-trip of
// a restored snapshot with every number as a float64, whatever an explainer wrote.
// declared.go already carries an int-then-float64 fallback in place for the same
// reason on Fact.Props, which is the mistake these prevent from spreading.

func TestMetricAccessors_TolerateTheRoundTrip(t *testing.T) {
	written := Insight{Metrics: map[string]any{
		"modules_scanned": 88,
		"share":           0.42,
		"layers_ordered":  []string{"ui", "domain", "data"},
		"layer_examples":  map[string]string{"ui": "feature/ui"},
	}}

	raw, err := json.Marshal(written)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored Insight
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for name, in := range map[string]Insight{"written": written, "restored": restored} {
		if got := in.MetricInt("modules_scanned"); got != 88 {
			t.Errorf("%s: MetricInt = %d, want 88", name, got)
		}
		if got := in.MetricFloat("share"); got != 0.42 {
			t.Errorf("%s: MetricFloat = %v, want 0.42", name, got)
		}
		if got := in.MetricStrings("layers_ordered"); len(got) != 3 || got[0] != "ui" || got[2] != "data" {
			t.Errorf("%s: MetricStrings = %v, want [ui domain data]", name, got)
		}
		if got := in.MetricStringMap("layer_examples"); got["ui"] != "feature/ui" {
			t.Errorf("%s: MetricStringMap = %v, want ui->feature/ui", name, got)
		}
	}

	// An integer written for a fractional metric, and the reverse, are both read
	// rather than silently dropped.
	whole := Insight{Metrics: map[string]any{"share": 1}}
	if got := whole.MetricFloat("share"); got != 1 {
		t.Errorf("MetricFloat over an int = %v, want 1", got)
	}
	if got := (Insight{Metrics: map[string]any{"n": 3.0}}).MetricInt("n"); got != 3 {
		t.Errorf("MetricInt over a float = %d, want 3", got)
	}
}

func TestMetricAccessors_AbsentAndWrongType(t *testing.T) {
	cases := map[string]Insight{
		"nil map":   {},
		"empty":     {Metrics: map[string]any{}},
		"wrong":     {Metrics: map[string]any{"modules_scanned": "eighty-eight", "layers_ordered": 7, "layer_examples": "no"}},
		"nil value": {Metrics: map[string]any{"modules_scanned": nil}},
	}
	for name, in := range cases {
		if got := in.MetricInt("modules_scanned"); got != 0 {
			t.Errorf("%s: MetricInt = %d, want 0", name, got)
		}
		if got := in.MetricFloat("modules_scanned"); got != 0 {
			t.Errorf("%s: MetricFloat = %v, want 0", name, got)
		}
		if got := in.MetricStrings("layers_ordered"); got != nil {
			t.Errorf("%s: MetricStrings = %v, want nil", name, got)
		}
		if got := in.MetricStringMap("layer_examples"); got != nil {
			t.Errorf("%s: MetricStringMap = %v, want nil", name, got)
		}
	}

	// A list whose elements are not all strings yields the ones that are: a partial
	// list is usable, and a caller cannot act on the difference anyway.
	mixed := Insight{Metrics: map[string]any{"layers_ordered": []any{"ui", 3, "data"}}}
	if got := mixed.MetricStrings("layers_ordered"); len(got) != 2 || got[0] != "ui" || got[1] != "data" {
		t.Errorf("MetricStrings over a mixed list = %v, want [ui data]", got)
	}
}

// An insight with no metrics must not grow a "metrics" key in the artifact.
func TestMetrics_OmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(Insight{Title: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "metrics") {
		t.Errorf("empty metrics serialized: %s", raw)
	}
}
