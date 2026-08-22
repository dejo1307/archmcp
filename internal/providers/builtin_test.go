package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/providers/rubydex"
)

// An entry without a command is valid only when the binary carries a
// provider of that name; anything else is a config error that names the
// built-ins, not a silent skip at snapshot time.
func TestValidate_NoCommandNamesABuiltIn(t *testing.T) {
	if err := Validate([]Provider{{Name: "rubydex"}}); err != nil {
		t.Fatalf("a built-in needs no command: %v", err)
	}
	err := Validate([]Provider{{Name: "nope"}})
	if err == nil || !strings.Contains(err.Error(), "built-ins: rubydex") {
		t.Fatalf("an unknown name without a command must be refused and the built-ins named, got %v", err)
	}
}

// Without the library in the cache the built-in is a named skip that says
// which command installs it; the snapshot never fails and never fetches.
func TestRun_BuiltInWithoutItsLibraryIsANamedSkip(t *testing.T) {
	if _, installed := rubydex.Installed(); installed {
		t.Skip("the Rubydex library is installed here, so the skip cannot be observed")
	}
	_, records := Run(context.Background(), []Provider{{Name: "rubydex"}}, t.TempDir(), nil, nil)
	if len(records) != 1 || !records[0].Skipped || !strings.Contains(records[0].Reason, rubydex.FetchHint) {
		t.Fatalf("records = %+v, want one skip naming %q", records, rubydex.FetchHint)
	}
}

func TestRun_UnknownBuiltInIsANamedSkip(t *testing.T) {
	_, records := Run(context.Background(), []Provider{{Name: "nope"}}, t.TempDir(), nil, nil)
	if len(records) != 1 || !records[0].Skipped || !strings.Contains(records[0].Reason, "no built-in provider") {
		t.Fatalf("records = %+v", records)
	}
}
