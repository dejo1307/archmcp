package cli

import "testing"

func TestShouldShowDashboardHintOnlyForInteractiveOutput(t *testing.T) {
	tests := []struct {
		name         string
		ci, disabled string
		outputTTY    bool
		want         bool
	}{
		{name: "terminal", outputTTY: true, want: true},
		{name: "CI", ci: "true", outputTTY: true},
		{name: "disabled", disabled: "1", outputTTY: true},
		{name: "redirected", outputTTY: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldShowDashboardHint(tt.ci, tt.disabled, tt.outputTTY); got != tt.want {
				t.Fatalf("shouldShowDashboardHint() = %v, want %v", got, tt.want)
			}
		})
	}
}
