package command

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/pkg/history"
)

// Log is `enola log`: the architecture history of a repository, newest last.
//
// It is a pure READER. It never snapshots, never writes, and never touches the repository
// — which is what makes it safe to run against a checkout you do not own, and what keeps
// it honest about the one thing it reports: what enola has actually observed. A `log`
// that quietly took a snapshot to fill in a gap would be inventing the history it claims
// to be reading.
//
// EXPERIMENTAL. Recording is on by default (see config.HistoryConfig for why a feature
// like this cannot usefully be opt-in), so the common outcome on a repository enola has
// snapshotted before is a timeline, and on one it has not is a line saying to snapshot it.
func (r *Runner) Log(args []string) {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		oneline = fs.Bool("oneline", true, "one line per revision (currently the only format)")
		limit   = fs.Int("n", 20, "show at most this many revisions (0 for all)")
		asJSON  = fs.Bool("json", false, "emit the entries as JSON")
		all     = fs.Bool("all", false, "include working revisions (uncommitted trees)")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr,
			"Usage: "+r.name()+" log [flags] [repo_path|config_path]\n\n"+
				"Show what this repository's architecture has done over time: one line per\n"+
				"snapshot enola recorded, with what changed since the one before it.\n\n"+
				"Read-only. It reports what was observed and never snapshots to fill a gap.\n\n"+
				"EXPERIMENTAL. Every snapshot is recorded as a revision; turn that off with\n"+
				"`history:` / `enabled: false` in your config.\n\n"+
				"Flags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	_ = oneline // the only format in this phase; the flag exists so scripts can be explicit

	var arg string
	if rest := fs.Args(); len(rest) > 0 {
		arg = rest[0]
	}
	repoPath, cfg := r.logTarget(arg)

	root, err := history.Root(repoPath, cfg.History.Dir)
	if err != nil {
		r.logFatal("cannot locate the history: %v", err)
	}
	entries, err := history.Read(root)
	if err != nil {
		if errors.Is(err, history.ErrNoHistory) {
			r.reportNoHistory(repoPath, root, cfg)
			return
		}
		r.logFatal("%v", err)
	}

	recorded := len(entries)
	if !*all {
		entries = onlyCommitted(entries)
	}
	if *limit > 0 && len(entries) > *limit {
		entries = entries[len(entries)-*limit:]
	}

	if *asJSON {
		out, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			r.logFatal("failed to encode the history: %v", err)
		}
		fmt.Println(string(out))
		return
	}
	if len(entries) == 0 {
		// Say how much was filtered out. "Nothing here" and "nothing here that you asked
		// to see" are different situations, and on a dirty tree — which is where anybody
		// running an agent loop spends their time — the second is the common one.
		fmt.Fprintf(os.Stderr,
			"No committed revisions recorded yet (%d working revision%s hidden — pass --all to see them).\n",
			recorded, pluralS(recorded))
		return
	}
	fmt.Print(renderOneline(entries))
}

// logTarget resolves the positional argument to a repository and the config that governs
// it, WITHOUT constructing an engine.
//
// resolveTarget (used by check/coverage) builds one, because those commands snapshot. A
// reader that built an engine would register every extractor and load every grammar to
// print twenty lines of text — and, worse, would make `enola log` fail on a repository
// whose config enola cannot fully honour, which is exactly a repository whose history
// somebody might want to look at.
func (r *Runner) logTarget(arg string) (string, *config.Config) {
	cfgPath := "mcp-arch.yaml"
	repoOverride := ""

	switch {
	case arg == "":
	case isDirectory(arg):
		abs, err := filepath.Abs(arg)
		if err != nil {
			r.logFatal("resolving repo path %q: %v", arg, err)
		}
		repoOverride = abs
		if inner := filepath.Join(abs, "mcp-arch.yaml"); fileExists(inner) {
			cfgPath = inner
		}
	default:
		cfgPath = arg
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		// A missing config is the ordinary case (built-in defaults); anything else is
		// worth saying out loud, but is still not a reason to refuse to read a history.
		cfg = config.Default()
	}
	if repoOverride != "" {
		cfg.Repo = repoOverride
		cfg.Repos = nil
	}
	if err := cfg.Normalize(); err != nil {
		r.logFatal("invalid config: %v", err)
	}

	repoPaths, err := cfg.RepoPaths()
	if err != nil || len(repoPaths) == 0 {
		r.logFatal("failed to resolve repo path: %v", err)
	}
	// The FIRST repo of a cluster owns the history: it is the one a multi-repo snapshot
	// is generated from, so it is where the graph's revisions were recorded.
	return repoPaths[0], cfg
}

// reportNoHistory explains an empty history in terms of what to do about it, and
// distinguishes the two ways of getting here. Recording is on by default, so the usual
// case is simply that this repository has not been snapshotted since — the remedy is a
// snapshot, not a setting. Being told to enable something that is already enabled is how
// a diagnostic becomes noise.
func (r *Runner) reportNoHistory(repoPath, root string, cfg *config.Config) {
	fmt.Fprintf(os.Stderr, "No architecture history for %s.\n", repoPath)
	if !cfg.HistoryEnabled() {
		fmt.Fprintf(os.Stderr,
			"\nRecording is turned off in %s (history.enabled: false).\n"+
				"Remove that line, or set it to true, and every snapshot after it becomes a revision.\n",
			configToEdit(repoPath, cfg))
		return
	}
	fmt.Fprintf(os.Stderr,
		"\nRecording is on — nothing has been snapshotted yet.\n"+
			"Run `%s --generate` and the first revision lands in\n%s\n", r.name(), root)
}

// configToEdit names the file the user should turn recording on in.
//
// The loaded config is the obvious candidate and is the WRONG answer in a case that is
// easy to hit: `enola log /some/other/repo` run from a directory that has its own
// mcp-arch.yaml loads THAT config (the same fallback `check` uses deliberately), so the
// advice would tell somebody to enable history in a config belonging to a repository they
// were not asking about — and doing as they were told would record a different repo's
// history. When the config that was loaded does not live inside the repository being
// reported on, name the file inside it instead.
func configToEdit(repoPath string, cfg *config.Config) string {
	if cfg.SourcePath != "" {
		if abs, err := filepath.Abs(cfg.SourcePath); err == nil {
			if rel, err := filepath.Rel(repoPath, abs); err == nil && !strings.HasPrefix(rel, "..") {
				return cfg.SourcePath
			}
		}
	}
	return filepath.Join(repoPath, "mcp-arch.yaml")
}

// onlyCommitted drops unanchored revisions — the working-tree snapshots an agent loop
// produces between commits. They are the majority of entries during a session and the
// minority of what anybody wants to read afterwards, which is why they are opt-in rather
// than filtered out by eye.
func onlyCommitted(entries []history.Entry) []history.Entry {
	out := make([]history.Entry, 0, len(entries))
	for _, e := range entries {
		if !e.Working() {
			out = append(out, e)
		}
	}
	return out
}

// renderOneline prints one revision per line, oldest first — the same direction as
// `git log --reverse`, because a history read forward is a story and read backward is a
// changelog, and this is the one that answers "how did it get like this?".
//
// Columns: short id · date · decorations · headline. An epoch change gets its own line
// above the revision that opens it, since a delta across that seam is rebuild noise and a
// reader who is not told that will read it as somebody's change.
func renderOneline(entries []history.Entry) string {
	var b strings.Builder
	prevEpoch := ""
	for i, e := range entries {
		if i > 0 && e.Epoch != prevEpoch {
			fmt.Fprintf(&b, "  ══ epoch changed (%s → %s) — the delta below is rebuild noise, not a change to the code\n",
				shortEpoch(prevEpoch), shortEpoch(e.Epoch))
		}
		prevEpoch = e.Epoch

		fmt.Fprintf(&b, "%-7s  %s  %s%s\n", e.Short(), shortDate(e.At), decorations(e), e.Summary.Headline())
	}
	return b.String()
}

// decorations is the bracketed context before the headline: the commit, the branch, any
// refs, and whether the tree was dirty. Empty when there is nothing to say.
func decorations(e history.Entry) string {
	var parts []string
	if c := e.Commit(); c != "" {
		parts = append(parts, shortCommit(c))
	}
	if ref := e.Ref(); ref != "" {
		parts = append(parts, ref)
	}
	parts = append(parts, e.Refs...)
	if e.Working() && e.Commit() != "" {
		parts = append(parts, "dirty")
	}
	if e.Summary.Incomparable {
		parts = append(parts, "incomparable")
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")  "
}

func shortEpoch(epoch string) string {
	if len(epoch) > 6 {
		return epoch[:6]
	}
	return epoch
}

// shortDate renders an RFC3339 timestamp as "2006-01-02 15:04", falling back to the raw
// string so an unparseable timestamp is shown rather than blanked.
func shortDate(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("2006-01-02 15:04")
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (r *Runner) logFatal(format string, args ...any) { r.cmdFatal("log", format, args...) }
