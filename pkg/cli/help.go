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
			bin.Name + " [flags] [config_path]",
		},
		Flags: []FlagDoc{
			{Flag: "--generate", Desc: "Generate a snapshot and exit (do not start MCP server)"},
			{Flag: "--explain [path]", Desc: "Print a human-readable repository statistics report and exit"},
			{Flag: "--list", Desc: "List the MCP tools this build can serve"},
			{Flag: "--status", Desc: "Show MCP server status: uptime, tool usage and estimated value,\naggregated across every repo the server has served."},
			{Flag: "--status --all", Desc: "Show the per-repo breakdown instead (from ~/.enola/usage/)"},
			{Flag: "--version", Desc: "Print version information"},
			{Flag: "--help, -h", Desc: "Show this help message"},
		},
		ConfigDoc: "Path to the config file (default: mcp-arch.yaml)",
		Examples: []Example{
			{Comment: "Start MCP server with default config", Command: bin.Name},
			{Comment: "Start MCP server with custom config", Command: bin.Name + " my-config.yaml"},
			{Comment: "Generate a snapshot and exit", Command: bin.Name + " --generate"},
			{Comment: "Print a statistics report for a repository and exit", Command: bin.Name + " --explain /path/to/repo"},
			{Comment: "Generate snapshot with custom config", Command: bin.Name + " --generate my-config.yaml"},
			{Comment: "Check MCP server status", Command: bin.Name + " --status"},
			{Comment: "Show usage broken down per repo", Command: bin.Name + " --status --all"},
			{Comment: "Check version", Command: bin.Name + " --version"},
		},
		Sections: []Section{
			mcpConfigSection(bin),
			buildSection(bin),
		},
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
