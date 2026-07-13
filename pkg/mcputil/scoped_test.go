package mcputil

import "testing"

func TestScope_HeadlineNamesTheFilter(t *testing.T) {
	population := []int{1, 2, 3, 4, 5, 6}

	tests := []struct {
		name     string
		filtered []int
		filter   string
		want     string
	}{
		// Unfiltered: the population IS the result, so there is nothing to qualify.
		{
			name:     "unfiltered",
			filtered: population,
			filter:   "",
			want:     "6 findings.",
		},
		// Filtered and non-empty: the caller must be able to see that a filter was
		// applied, and how big the set it was drawn from is.
		{
			name:     "filtered",
			filtered: []int{1, 2},
			filter:   `package="androidTest"`,
			want:     `2 findings under package="androidTest" (of 6 repo-wide).`,
		},
		// The case this type exists for. A bare "0 findings." is indistinguishable
		// from a broken filter; naming the filter and the population makes the zero
		// legible. Reporting the POPULATION count here — which is what
		// analyze_performance did — is the defect: it said "61 findings" beside an
		// empty table.
		{
			name:     "filtered to empty",
			filtered: nil,
			filter:   `package="androidTest"`,
			want:     `0 findings under package="androidTest" (of 6 repo-wide).`,
		},
		{
			name:     "unfiltered and empty",
			filtered: nil,
			filter:   "",
			want:     "0 findings.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Scope(len(population), tt.filtered, tt.filter)
			if got := s.Headline("findings"); got != tt.want {
				t.Errorf("Headline() = %q, want %q", got, tt.want)
			}
			if len(s.Items) != len(tt.filtered) {
				t.Errorf("Items = %d, want %d", len(s.Items), len(tt.filtered))
			}
		})
	}
}

// TestScope_ItemsAreTheOnlyCountableSet is the invariant, stated as a test: a
// renderer holding a Scoped can only count the filtered set. Population is an int,
// not a slice, so there is no unfiltered corpus to accidentally range over — which
// is exactly how analyze_performance came to report repo-wide totals beside a
// filtered list.
func TestScope_ItemsAreTheOnlyCountableSet(t *testing.T) {
	s := Scope(100, []string{"a", "b"}, `repo="x"`)
	if len(s.Items) != 2 {
		t.Fatalf("Items = %d, want 2", len(s.Items))
	}
	if s.Population != 100 {
		t.Errorf("Population = %d, want 100", s.Population)
	}
	if !s.Filtered() {
		t.Errorf("Filtered() = false, want true")
	}
	if Scope(2, []string{"a", "b"}, "").Filtered() {
		t.Errorf("Filtered() = true for an unfiltered scope")
	}
}
