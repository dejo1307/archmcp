package cli

import (
	"fmt"
	"io"
	"strings"
)

// descColumn is the column at which a flag's or command's description starts.
// Continuation lines in a multi-line description are indented to match.
const descColumn = 22

// FlagDoc documents one flag or command. Desc may span several lines; the
// renderer indents continuations to the description column.
type FlagDoc struct {
	Flag string
	Desc string
}

// Section is a free-form trailing block of the help text (LICENSE, BUILD, …).
// Body is emitted verbatim, so it carries its own indentation.
type Section struct {
	Title string
	Body  string
}

// Example is one entry of the EXAMPLES block: a comment line and the command
// it introduces.
type Example struct {
	Comment string
	Command string
}

// Binary identifies the binary the help text is describing. It is what makes
// the shared help reusable: everything a wrapper needs to rename is in here.
type Binary struct {
	Name       string // executable name, e.g. "enola"
	CmdPackage string // go build target, e.g. "./cmd/enola"
	VersionVar string // ldflags -X target for the version string

	// BuildOutput is the artifact name a source build produces, when it differs
	// from Name (e.g. "enola-ent" for enola-enterprise). Defaults to Name.
	BuildOutput string
}

// output returns the artifact name to show in the BUILD section.
func (b Binary) output() string {
	if b.BuildOutput != "" {
		return b.BuildOutput
	}
	return b.Name
}

// HelpSpec is the rendered form of `--help`. A wrapper binary starts from
// DefaultHelp and appends to Commands/Flags/Sections rather than restating the
// shared text.
type HelpSpec struct {
	Bin       Binary
	Tagline   string    // one-line description, printed beside the binary name
	Intro     string    // short paragraph under the title
	Usage     []string  // USAGE lines, already prefixed with the binary name
	Commands  []FlagDoc // COMMANDS block; omitted when empty
	Flags     []FlagDoc // FLAGS block
	ConfigDoc string    // CONFIG_PATH block body (one line); omitted when empty
	Examples  []Example // EXAMPLES block
	Sections  []Section // trailing blocks, in order
}

// DefaultHelp returns the help shared by every enola binary. It documents only
// what the engine itself provides — it never mentions licensing, activation or
// anything a wrapper adds.
func DefaultHelp(bin Binary) HelpSpec {
	return HelpSpec{
		Bin:     bin,
		Tagline: "MCP server for architectural snapshots",
		Intro:   "Give your AI agent a map of the codebase before it starts exploring.",
		Usage: []string{
			bin.Name + " [flags] [repo_path|config_path]",
			bin.Name + " baseline <pin|show|clear> [repo_path|config_path]",
			bin.Name + " check [flags] [repo_path|config_path]",
			bin.Name + " install [--hooks] [--global] [repo_path]",
		},
		Commands: []FlagDoc{
			{Flag: "install", Desc: "Write " + bin.Name + "'s instructions into the files your coding\nagents read (Claude Code, Cursor, AGENTS.md). Previews every\nchange and asks before writing.\n  --hooks   also run the loop automatically: report the\n            architectural delta at the end of a session, but only\n            if the change introduced a regression. Opt-in,\n            because hooks run commands.\n  --global  configure this user rather than this repository.\n  --dry-run show what would change and write nothing."},
			{Flag: "uninstall", Desc: "Remove everything \"install\" wrote, leaving the rest of each\nfile byte-for-byte as it was."},
			{Flag: "baseline", Desc: "Manage the diff baseline — the \"before\" your changes are graded\nagainst. \"pin\" snapshots the repository and freezes it (no separate\n--generate needed), \"show\" reports what the current baseline\ndescribes, \"clear\" removes it. The baseline is stored per-repository,\nin that repo's output dir, so several repos each keep their own."},
			{Flag: "check", Desc: "Grade what a change did to the architecture against the pinned\nbaseline, and exit with a code CI can act on:\n  0 clean · 1 regression · 2 error · 3 declined (not comparable)\nRead-only by default — nothing is written, and the baseline stays\nput, so it can be run as often as you like. Run\n\"" + bin.Name + " check --help\" for the flags."},
		},
		Flags: []FlagDoc{
			{Flag: "--generate", Desc: "Generate a snapshot and exit (do not start MCP server)"},
			{Flag: "--explain [path]", Desc: "Print a human-readable repository statistics report and exit.\nA directory is a repository; a file is a config, so a `repos:`\nconfig reports over the whole cluster."},
			{Flag: "--list", Desc: "List the MCP tools this build can serve"},
			{Flag: "--status", Desc: "Show MCP server status: uptime, tool usage and estimated value,\naggregated across every repo the server has served. While the server\nis running this also prints the dashboard URL."},
			{Flag: "--status --all", Desc: "Show the per-repo breakdown instead (from ~/.enola/usage/)"},
			{Flag: "--no-dashboard", Desc: "Do not start the localhost dashboard alongside the MCP server"},
			{Flag: "--version", Desc: "Print version information"},
			{Flag: "--help, -h", Desc: "Show this help message"},
		},
		ConfigDoc: "Path to the config file (default: mcp-arch.yaml). Set `repos:` in it to\n  name a multi-repo cluster; entries resolve relative to the config file, so\n  a checked-in cluster config means the same thing wherever it is run from.",
		Examples: []Example{
			{Comment: "Start MCP server with default config", Command: bin.Name},
			{Comment: "Start MCP server with custom config", Command: bin.Name + " my-config.yaml"},
			{Comment: "Generate a snapshot and exit", Command: bin.Name + " --generate"},
			{Comment: "Index a whole cluster from one config (see CONFIG_PATH)", Command: bin.Name + " --generate cluster.yaml"},
			{Comment: "Print a statistics report for a repository and exit", Command: bin.Name + " --explain /path/to/repo"},
			{Comment: "Report over a whole cluster", Command: bin.Name + " --explain cluster.yaml"},
			{Comment: "Generate snapshot with custom config", Command: bin.Name + " --generate my-config.yaml"},
			{Comment: "Freeze another repo's architecture before editing it (see THE GATE)", Command: bin.Name + " baseline pin /path/to/repo"},
			{Comment: "Grade your changes to it (exit 1 on a structural regression)", Command: bin.Name + " check /path/to/repo"},
			{Comment: "Report what the pinned baseline describes", Command: bin.Name + " baseline show /path/to/repo"},
			{Comment: "Tell your agents enola is here (instructions only)", Command: bin.Name + " install"},
			{Comment: "…and close the loop automatically at the end of a session", Command: bin.Name + " install --hooks"},
			{Comment: "Preview what install would change, writing nothing", Command: bin.Name + " install --hooks --dry-run"},
			{Comment: "Remove it all again", Command: bin.Name + " uninstall"},
			{Comment: "Report a regression without failing the build", Command: bin.Name + " check --warn-only"},
			{Comment: "Compare against the preceding snapshot instead of the pin", Command: bin.Name + " check --baseline=previous"},
			{Comment: "Also fail on new layer violations", Command: bin.Name + " check --fail-on=cycles,layers --min-confidence=0.8"},
			{Comment: "Check MCP server status", Command: bin.Name + " --status"},
			{Comment: "Show usage broken down per repo", Command: bin.Name + " --status --all"},
			{Comment: "Check version", Command: bin.Name + " --version"},
		},
		Sections: []Section{
			gateSection(bin),
			dashboardSection(),
			mcpConfigSection(bin),
			buildSection(bin),
		},
	}
}

// gateSection documents the before/after loop. It is a section rather than a list of
// examples because the ORDER is the whole point: the baseline has to be pinned before the
// edit, and a reader who finds `check` first will otherwise run it against nothing.
func gateSection(bin Binary) Section {
	return Section{
		Title: "THE GATE — grading a change",
		Body: fmt.Sprintf(`  Tests tell you whether behaviour still works. This tells you whether a change
  altered the STRUCTURE of the system in a way nobody asked for — a new dependency
  cycle, coupling between modules that had no business knowing about each other.

  It needs a "before" to compare against, so the order matters:

    1.  %s baseline pin /path/to/repo     # freeze how it looks NOW, before editing
    2.  …make your changes…
    3.  %s check /path/to/repo            # grade what they did

  Step 1 snapshots the repository and freezes it — no separate --generate needed.
  Omit the path to act on the current directory.

  The baseline lives in that repository's own output dir (.enola/baseline), so each
  repo keeps its own and you can hold baselines for several at once. "baseline show"
  reports what the current one describes; "baseline clear" removes it.

  Step 3 is read-only: it writes nothing and leaves the baseline in place, so it can
  be run as often as you like and re-run after a fix. It exits 0 when clean, 1 on a
  structural regression, 2 when it could not run, and 3 when it declined to grade
  because the baseline is not comparable — 3 is never a statement about your change.

  A path that names a DIRECTORY is a repository; one that names a FILE is a config.
`, bin.Name, bin.Name),
	}
}

// dashboardSection documents the read-only HTTP dashboard served alongside the
// MCP server.
func dashboardSection() Section {
	return Section{
		Title: "DASHBOARD",
		Body: `  When the MCP server starts, a read-only HTTP dashboard is served on a free
  localhost port (127.0.0.1). It shows the same status data plus the snapshot and
  graph receipts, and refreshes every 30 seconds. Run "--status" while the server
  is up to get its URL, or pass "--no-dashboard" to skip it entirely.
`,
	}
}

// mcpConfigSection documents how to register the binary with an MCP client.
func mcpConfigSection(bin Binary) Section {
	return Section{
		Title: "MCP CONFIGURATION",
		Body: fmt.Sprintf(`  Add to your MCP client's config (e.g., Cursor mcp.json):

    {
      "mcpServers": {
        "enola": {
          "command": "%s",
          "args": ["mcp-arch.yaml"]
        }
      }
    }

  Or if installed globally via "go install":

    {
      "mcpServers": {
        "enola": {
          "command": "%s"
        }
      }
    }
`, bin.Name, bin.Name),
	}
}

// buildSection documents the version ldflag for a source build.
func buildSection(bin Binary) Section {
	return Section{
		Title: "BUILD",
		Body: fmt.Sprintf(`  Version can be set at build time with ldflags:

    go build -ldflags "-X %s=0.1.0" -o %s %s
`, bin.VersionVar, bin.output(), bin.CmdPackage),
	}
}

// AppendFlagNote appends note as extra lines to an existing flag's description,
// so a wrapper can qualify a shared flag without restating the Flags slice. It
// is a no-op when the flag is absent.
func (s *HelpSpec) AppendFlagNote(flag, note string) {
	for i := range s.Flags {
		if s.Flags[i].Flag == flag {
			s.Flags[i].Desc += "\n" + note
			return
		}
	}
}

// InsertSectionsBefore inserts sections immediately before the section with the
// given title, so a wrapper can place its own blocks precisely rather than only
// at the end. Sections are appended when the title is not found.
func (s *HelpSpec) InsertSectionsBefore(title string, secs ...Section) {
	for i, sec := range s.Sections {
		if sec.Title == title {
			rest := append([]Section{}, s.Sections[i:]...)
			s.Sections = append(append(s.Sections[:i:i], secs...), rest...)
			return
		}
	}
	s.Sections = append(s.Sections, secs...)
}

// RenderHelp writes the help text for spec to w.
func RenderHelp(w io.Writer, spec HelpSpec) {
	var b strings.Builder

	fmt.Fprintf(&b, "%s — %s\n\n%s\n", spec.Bin.Name, spec.Tagline, spec.Intro)

	if len(spec.Usage) > 0 {
		b.WriteString("\nUSAGE\n")
		for _, u := range spec.Usage {
			fmt.Fprintf(&b, "  %s\n", u)
		}
	}
	writeDocs(&b, "COMMANDS", spec.Commands)
	writeDocs(&b, "FLAGS", spec.Flags)

	if spec.ConfigDoc != "" {
		fmt.Fprintf(&b, "\nCONFIG_PATH\n  %s\n", spec.ConfigDoc)
	}

	if len(spec.Examples) > 0 {
		b.WriteString("\nEXAMPLES\n")
		for i, ex := range spec.Examples {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "  # %s\n  %s\n", ex.Comment, ex.Command)
		}
	}

	for _, sec := range spec.Sections {
		fmt.Fprintf(&b, "\n%s\n%s", sec.Title, sec.Body)
	}

	// Help goes to a terminal stream; a write failure has nowhere useful to go.
	_, _ = io.WriteString(w, b.String())
}

// writeDocs renders a titled block of flag/command documentation, wrapping each
// description's continuation lines to the description column.
func writeDocs(b *strings.Builder, title string, docs []FlagDoc) {
	if len(docs) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s\n", title)
	for _, d := range docs {
		lines := strings.Split(d.Desc, "\n")
		fmt.Fprintf(b, "  %-*s%s\n", descColumn-2, d.Flag, lines[0])
		for _, cont := range lines[1:] {
			fmt.Fprintf(b, "%s%s\n", strings.Repeat(" ", descColumn), cont)
		}
	}
}
