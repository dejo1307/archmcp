package main

import (
	"reflect"
	"testing"
)

// The memory flags are stripped before every other parser in this binary sees the
// argument list, so the risk they carry is not their own behaviour but what they
// do to unrelated flags: drop one, keep a path that should have been consumed, or
// reorder the rest, and `--generate <repo>` silently analyses the wrong directory.
func TestSplitMemFlags(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		wantRest []string
		wantPath string
		wantOn   bool
	}{
		{
			name:     "absent leaves the arguments untouched",
			argv:     []string{"--generate", "/repo"},
			wantRest: []string{"--generate", "/repo"},
		},
		{
			name:     "memstats is removed, nothing else is",
			argv:     []string{"--generate", "--memstats", "/repo"},
			wantRest: []string{"--generate", "/repo"},
			wantOn:   true,
		},
		{
			name:     "memprofile takes the following argument with it",
			argv:     []string{"--memprofile", "heap.pb.gz", "--generate", "/repo"},
			wantRest: []string{"--generate", "/repo"},
			wantPath: "heap.pb.gz",
			wantOn:   true,
		},
		{
			// The value is consumed positionally, so a path that looks like a flag
			// must still be taken as the path rather than re-parsed.
			name:     "a flag-shaped path is still the path",
			argv:     []string{"--memprofile", "--generate", "/repo"},
			wantRest: []string{"/repo"},
			wantPath: "--generate",
			wantOn:   true,
		},
		{
			name:     "both together",
			argv:     []string{"--memstats", "--memprofile", "h.pb.gz", "--explain"},
			wantRest: []string{"--explain"},
			wantPath: "h.pb.gz",
			wantOn:   true,
		},
		{
			name:     "empty argument list",
			argv:     nil,
			wantRest: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rest, path, on, err := splitMemFlags(tt.argv)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("rest = %q, want %q", rest, tt.wantRest)
			}
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
			if on != tt.wantOn {
				t.Errorf("enabled = %v, want %v", on, tt.wantOn)
			}
		})
	}
}

// A trailing --memprofile must fail loudly. Silently downgrading to --memstats
// would produce a benchmark run that looks instrumented and wrote no profile.
func TestSplitMemFlags_MissingPath(t *testing.T) {
	if _, _, _, err := splitMemFlags([]string{"--generate", "/repo", "--memprofile"}); err == nil {
		t.Fatal("expected an error for --memprofile with no path")
	}
}
