package command

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/cli"
)

// An operational failure is where the notice EARNS its place: a build whose extractors
// are behind detects no language where a current one detects several, and the run dies
// with "snapshot produced no facts" — an error about the repository, whose actual cause
// is the binary. So the fatal path carries the notice too, and this test is what keeps it
// there, because nothing about the code reads as though it should.
func TestFatalCarriesTheUpdateNotice(t *testing.T) {
	seedUpdateCache(t, "0.3.12", "v999")

	var sb strings.Builder
	New(cli.Binary{Name: "enola"}, "upgrade").
		writeFatal(&sb, "check", "snapshot produced no facts for %s", "/repo")
	out := sb.String()

	if !strings.Contains(out, "enola check: snapshot produced no facts for /repo") {
		t.Fatalf("the failure itself is missing:\n%s", out)
	}
	if !strings.Contains(out, "v0.3.12 is available") {
		t.Fatalf("no update notice on the fatal path, which is the path most likely to be caused by one:\n%s", out)
	}
	if !strings.Contains(out, "extractors changed") {
		t.Errorf("the extractor escalation is dropped here, and it is the half that explains a no-facts failure:\n%s", out)
	}

	// Order is load-bearing. What failed is what they came for; the notice is context for
	// it, and a housekeeping line printed first reads as the error.
	if strings.Index(out, "produced no facts") > strings.Index(out, "is available") {
		t.Errorf("the update notice precedes the failure it annotates:\n%s", out)
	}
}

// The other half of the contract: an unremarkable failure on an up-to-date build stays a
// bare error. This is what stops the fatal path from growing a permanent second line.
func TestFatalSaysNothingExtraWhenCurrent(t *testing.T) {
	seedUpdateCache(t, "", "") // isolated HOME, no cache — an offline or fresh install

	var sb strings.Builder
	New(cli.Binary{Name: "enola"}, "upgrade").
		writeFatal(&sb, "check", "%q is not a directory", "/nope")

	if got := sb.String(); got != "enola check: \"/nope\" is not a directory\n" {
		t.Errorf("fatal output = %q, want the error alone", got)
	}
}
