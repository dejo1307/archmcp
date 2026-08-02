package command

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/cli"
)

// Every command the shared help DOCUMENTS must be one some binary can actually RUN.
//
// This is the assertion that would have caught the defect this package exists to fix.
// cli.DefaultHelp is shared by every enola binary, so it advertised `check`, `baseline`,
// `coverage`, `doctor`, `install` and `uninstall` from the wrapper's --help too — while
// the wrapper dispatched none of them. Typing one did not produce an error: it fell
// through the wrapper's argument loop, was taken for a config path, and started an MCP
// server on stdio.
//
// Dispatchable means Subcommands() plus whatever the binary handles itself, since
// `upgrade` is documented by cmd/enola and dispatched there.
func TestDocumentedCommandsAreDispatchable(t *testing.T) {
	dispatchable := map[string]bool{}
	for _, c := range Subcommands() {
		dispatchable[c] = true
	}
	// What cmd/enola adds for itself.
	dispatchable["upgrade"] = true

	spec := cli.DefaultHelp(cli.Binary{Name: "enola"})
	for _, doc := range spec.Commands {
		// A command entry may carry an argument placeholder ("activate <KEY>"); the
		// command is the first word.
		name := strings.Fields(doc.Flag)[0]
		if !dispatchable[name] {
			t.Errorf("help documents %q but no binary dispatches it — typing it falls through to argument parsing", name)
		}
	}
}

// The converse: a command this package dispatches but nothing documents is unreachable
// in practice, since nobody is told it exists. `hook` is the deliberate exception — it is
// invoked by the agent harness from the config `install --hooks` writes, never typed.
func TestDispatchedCommandsAreDocumented(t *testing.T) {
	documented := map[string]bool{}
	for _, doc := range cli.DefaultHelp(cli.Binary{Name: "enola"}).Commands {
		documented[strings.Fields(doc.Flag)[0]] = true
	}
	undocumentedByDesign := map[string]bool{"hook": true}

	for _, c := range Subcommands() {
		if !documented[c] && !undocumentedByDesign[c] {
			t.Errorf("Dispatch handles %q but the shared help never mentions it", c)
		}
	}
}

// Usage lines and suggested remedies must name the binary that is running, not the one
// the code was written for. A wrapper telling its user to run `enola baseline pin` names
// a binary they may not have installed.
func TestDiagnosticsNameTheRunningBinary(t *testing.T) {
	r := New(cli.Binary{Name: "enola-enterprise"}, "upgrade")

	got := r.UnknownArgHelp("chekc")
	if !strings.Contains(got, "enola-enterprise chekc"[:len("enola-enterprise")]) {
		t.Errorf("UnknownArgHelp does not name the running binary:\n%s", got)
	}
	if strings.Contains(got, "`enola --help`") {
		t.Errorf("UnknownArgHelp suggests the OSS binary to a wrapper user:\n%s", got)
	}

	// The typo suggestion must still reach a command the wrapper dispatches.
	if !strings.Contains(got, `did you mean "check"`) {
		t.Errorf("UnknownArgHelp lost the near-miss suggestion:\n%s", got)
	}
}

// A binary's own subcommands are recognised for diagnostics but never dispatched here —
// otherwise the shared layer would claim to run something it has no implementation for.
func TestOwnSubcommandsAreNotDispatched(t *testing.T) {
	for _, c := range Subcommands() {
		if c == "upgrade" {
			t.Fatal("upgrade is OSS-only and must not be in the shared dispatch set")
		}
	}
	if got := New(cli.Binary{Name: "enola"}, "upgrade").closestSubcommand("upgrad"); got != "upgrade" {
		t.Errorf("closestSubcommand(%q) = %q, want %q — an own subcommand must still be suggested", "upgrad", got, "upgrade")
	}
}
