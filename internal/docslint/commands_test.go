package docslint

import (
	"regexp"
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/cli"
)

// commandTableDoc is the page a reader is sent to for "commands, flags, exit codes",
// and commandTableHeader is the row that opens its catalogue of commands.
//
// The table is found by its header rather than by section, because the page's own
// title contains the section's name — Doc.Section would match the H1 and hand back
// the whole document, which is a check over nothing dressed up as a check.
const (
	commandTableDoc    = "docs/CLI.md"
	commandTableHeader = "| Command | What it does |"
)

// cmdCellRe takes the first backticked span of a table row: `log [flags] [repo|config]`.
var cmdCellRe = regexp.MustCompile("^\\|\\s*`([^`]+)`")

// binaryOwnedCommands are dispatched by cmd/enola itself rather than by pkg/command,
// so cli.DefaultHelp — which is shared by every enola binary — does not carry them.
// docslint cannot import package main to ask, so the name is written down here and
// TestCommandTableNamesNothingUnknown is what keeps it honest.
var binaryOwnedCommands = map[string]bool{"upgrade": true}

// TestEveryCommandIsInTheCLITable is the missing third link in a chain that was
// already two-thirds built.
//
// pkg/command asserts help ↔ dispatch in both directions, and cmd/enola asserts
// flags ↔ help. Nothing tied either to the DOCUMENTATION, so `log`, `show`, `diff`,
// `blame`, `gc` and `history` — six of the seventeen subcommands, and the whole of
// the architecture-history feature — were absent from the CLI reference for as long
// as they had existed. Every other check in this repository passed.
//
// This is deliberately not one of the contracts in contract_test.go: those match an
// item anywhere in the prose, and a bare `log`, `show`, `diff` or `gc` occurs in
// ordinary English on nearly every page. Only the command column settles it.
func TestEveryCommandIsInTheCLITable(t *testing.T) {
	listed := commandTable(t)

	var documented []string
	for _, c := range cli.DefaultHelp(cli.Binary{Name: "enola"}).Commands {
		documented = append(documented, strings.Fields(c.Flag)[0])
	}
	if len(documented) < 10 {
		t.Fatalf("cli.DefaultHelp documents %d commands (%v); the help spec changed",
			len(documented), documented)
	}

	for _, name := range documented {
		if !listed[name] {
			t.Errorf("`enola --help` documents %q, but %s's command table never lists it.\n"+
				"    A command absent from that table is one a reader cannot discover:\n"+
				"    the docs index sends them there for commands, flags and exit codes.",
				name, commandTableDoc)
		}
	}
}

// TestCommandTableNamesNothingUnknown is the converse. A row for a command no binary
// dispatches sends the reader to a command that does not exist, and it is also what
// keeps binaryOwnedCommands from silently outliving the command it excuses.
func TestCommandTableNamesNothingUnknown(t *testing.T) {
	real := map[string]bool{}
	for _, c := range cli.DefaultHelp(cli.Binary{Name: "enola"}).Commands {
		real[strings.Fields(c.Flag)[0]] = true
	}
	for name := range binaryOwnedCommands {
		real[name] = true
	}

	listed := commandTable(t)
	for name := range listed {
		if !real[name] {
			t.Errorf("%s's command table lists %q, which `enola --help` does not document — "+
				"the command was renamed or removed, or it is dispatched by cmd/enola and "+
				"belongs in binaryOwnedCommands", commandTableDoc, name)
		}
	}
	for name := range binaryOwnedCommands {
		if !listed[name] {
			t.Errorf("binaryOwnedCommands excuses %q from the shared help, but %s no longer "+
				"lists it either — drop the entry", name, commandTableDoc)
		}
	}
}

// commandTable returns the command named by each row of the CLI reference's command
// table: the first word of the row's first backticked span.
func commandTable(t *testing.T) map[string]bool {
	t.Helper()

	var page string
	for _, d := range corpus(t) {
		if d.Path == commandTableDoc {
			page = d.Prose
		}
	}
	if page == "" {
		t.Fatalf("%s is not in the corpus", commandTableDoc)
	}

	lines := strings.Split(page, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == commandTableHeader {
			start = i + 2 // skip the header and its separator row
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s has no row %q — the table was reformatted, and this check now "+
			"reads nothing", commandTableDoc, commandTableHeader)
	}

	out := map[string]bool{}
	for _, l := range lines[start:] {
		if !strings.HasPrefix(l, "|") {
			break // the table ended
		}
		m := cmdCellRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		out[strings.Fields(m[1])[0]] = true
	}
	if len(out) < 10 {
		t.Fatalf("only %d commands parsed out of %s (%v); the table's shape changed",
			len(out), commandTableDoc, out)
	}
	return out
}
