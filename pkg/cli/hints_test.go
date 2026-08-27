package cli

import "testing"

func TestDashboardHint(t *testing.T) {
	if got, want := DashboardHint("enola", ""), "\nExplore this snapshot in your browser:\n  enola dashboard --open\nIt starts in the background; stop it later with: enola dashboard stop\n"; got != want {
		t.Fatalf("DashboardHint(no target) = %q, want %q", got, want)
	}
	if got, want := DashboardHint("enola", "../repo"), "\nExplore this snapshot in your browser:\n  enola dashboard --open \"../repo\"\nIt starts in the background; stop it later with: enola dashboard stop\n"; got != want {
		t.Fatalf("DashboardHint(with target) = %q, want %q", got, want)
	}
}

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
