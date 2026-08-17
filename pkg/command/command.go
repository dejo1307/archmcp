// Package command implements the subcommands every enola binary shares — the gate
// (`check`, `baseline`), the reports (`coverage`, `doctor`), the installer
// (`install`/`uninstall`) and the session hooks (`hook`).
//
// It exists because a wrapper binary cannot reach them otherwise. They are built on
// enola's internal packages — the engine's baseline resolution, the hook heartbeat, the
// file lock — none of which cross a module boundary, so a separate module can neither
// import them nor reimplement them without carrying a second copy of the gate's exit-code
// contract and the hook's silence rules. Keeping the commands here, INSIDE enola, lets
// them go on using those internals while a wrapper reaches them through one exported
// surface.
//
// Deliberately not folded into pkg/cli: that package is pure text (help and the tool
// catalogue) and anything wanting only `--help` would otherwise pull in the whole engine.
// This package imports pkg/cli, not the other way round.
package command

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/cli"
)

// Runner runs the shared subcommands on behalf of one binary.
//
// The binary is held rather than passed per call because it reaches almost every
// user-facing string: usage blocks, error prefixes, and — the part that matters — the
// commands suggested in remedies. A gate that tells an enola-enterprise user to run
// `enola baseline pin` names a binary they may not have installed.
type Runner struct {
	bin cli.Binary
	// own are subcommands the BINARY dispatches for itself, before delegating here.
	// They are not run by this package, but they are still part of what the binary
	// accepts, so they belong in the typo suggestions and the "expected one of" list —
	// otherwise `enola upgrad` is told upgrade is not a command of any kind.
	own []string
	// setup configures every engine the shared commands build, so a wrapper's plugins
	// are present in the gate, the hook and the reports — not only in its MCP server.
	//
	// These commands construct their OWN engines (a gate has to snapshot the tree it is
	// grading), so a wrapper that registered plugins on the server's engine alone got a
	// plain OSS engine here. That is how `baseline pin` came to write a snapshot with a
	// different explainer set than `--generate` on the same tree, from the same binary.
	setup func(*bootstrap.Engine)
}

// WithEngine registers a hook applied to every engine these commands construct. Returns
// the Runner so it can be chained onto New.
//
// The callback takes only the Engine: a wrapper outside this module cannot name
// *config.Config, but it can reach the same value through Engine.Config() and mutate it
// there. Keeping the signature to one exported type is what makes this usable across the
// module boundary at all.
func (r *Runner) WithEngine(setup func(*bootstrap.Engine)) *Runner {
	r.setup = setup
	return r
}

// newEngine builds an engine for a shared command and applies the wrapper's setup.
// Every command that needs an engine must go through here; calling bootstrap.NewEngine
// directly silently opts that command out of whatever the binary registers.
func (r *Runner) newEngine(opts bootstrap.Options) (*bootstrap.Engine, *config.Config, error) {
	eng, cfg, err := bootstrap.NewEngine(opts)
	if err != nil {
		return nil, nil, err
	}
	if r.setup != nil {
		r.setup(eng)
	}
	return eng, cfg, nil
}

// New returns a Runner for the given binary. ownSubcommands names commands the binary
// handles itself (cmd/enola passes "upgrade"); they are recognised for diagnostics but
// never dispatched here.
func New(bin cli.Binary, ownSubcommands ...string) *Runner {
	return &Runner{bin: bin, own: ownSubcommands}
}

// knownSubcommands is everything the binary accepts: what this package dispatches, plus
// whatever the binary dispatches for itself.
func (r *Runner) knownSubcommands() []string {
	return append(append([]string{}, Subcommands()...), r.own...)
}

// name is the binary as a user types it, used in usage lines and suggested commands.
func (r *Runner) name() string {
	if r.bin.Name == "" {
		return "enola"
	}
	return r.bin.Name
}

// Subcommands are the commands Dispatch handles. Exported so a binary's --help can be
// checked against what it can actually run: the shared help documents all of these, and
// a binary that advertises one it does not dispatch sends the caller into whatever its
// argument parser does with an unrecognised word.
//
// `upgrade` is deliberately absent. It is OSS-only — a wrapper ships through its own
// release path — so cmd/enola dispatches it itself, before calling Dispatch.
func Subcommands() []string {
	return []string{"check", "constraints", "plan", "coverage", "endpoint", "doctor", "baseline", "log", "show", "diff", "blame", "gc", "history", "install", "uninstall", "hook"}
}

// Dispatch runs the subcommand named by args[0], if it is one of Subcommands().
//
// It reports whether it handled the arguments. In practice a handled command exits the
// process — the gate exits with its verdict's code, the hook always exits 0 — so the
// false return is the load-bearing one: it tells the caller this was not a subcommand
// and its own flag parsing should continue.
//
// args is the argument list WITHOUT the program name (os.Args[1:]).
func (r *Runner) Dispatch(ctx context.Context, args []string) bool {
	if len(args) == 0 {
		return false
	}

	// Refresh the update cache from here, because this is the one place every shared
	// subcommand passes through: putting it in each command instead would make the notice
	// depend on which command someone happens to run, which is how it came to depend on
	// having agent hooks installed in the first place.
	//
	// Excluding `hook`: it refreshes inside the detached child it already starts, and its
	// contract is to add nothing to session-start latency — not even one spawn. Excluding
	// unrecognised arguments too, so a typo does not start a process before being told it
	// was a typo.
	if args[0] != "hook" && slices.Contains(Subcommands(), args[0]) {
		SpawnUpdateRefresh()
	}

	switch args[0] {
	case "check":
		r.Check(ctx, args[1:]) // exits with the verdict's code
	case "constraints":
		r.Constraints(args[1:]) // exits 0 valid, 1 problems, 2 could not run
	case "plan":
		r.Plan(ctx, args[1:])
		os.Exit(0)
	case "coverage":
		r.Coverage(ctx, args[1:])
		os.Exit(0)
	case "endpoint":
		r.Endpoint(ctx, args[1:])
		os.Exit(0)
	case "doctor":
		r.Doctor(args[1:])
		os.Exit(0)
	case "baseline":
		r.Baseline(args[1:])
		os.Exit(0)
	case "log":
		r.Log(args[1:])
		os.Exit(0)
	case "show":
		r.Show(args[1:])
		os.Exit(0)
	case "diff":
		r.Diff(args[1:])
		os.Exit(0)
	case "blame":
		r.Blame(args[1:])
		os.Exit(0)
	case "gc":
		r.GC(args[1:])
		os.Exit(0)
	case "history":
		r.History(args[1:])
		os.Exit(0)
	case "hook":
		r.Hook(ctx, args[1:]) // always exits 0; never disturbs a session
	case "install":
		r.Install(args[1:], false)
		os.Exit(0)
	case "uninstall":
		r.Install(args[1:], true)
		os.Exit(0)
	default:
		return false
	}
	return true
}

// UnknownArgHelp explains a rejected argument in terms of what the caller probably meant.
//
// The two cases worth separating are a mistyped subcommand and a bad path: telling
// someone who typed `enola chekc .` that "no such file or directory" would send them
// looking at their filesystem rather than at their spelling.
func (r *Runner) UnknownArgHelp(arg string) string {
	if !strings.HasPrefix(arg, "-") && !strings.ContainsAny(arg, "/\\.") {
		if near := r.closestSubcommand(arg); near != "" {
			return fmt.Sprintf("unknown command %q — did you mean %q?\n\n    %s %s --help\n\nRun `%s --help` for all commands.",
				arg, near, r.name(), near, r.name())
		}
		return fmt.Sprintf("unknown command %q (expected one of: %s), and it is not a path either.\n\nRun `%s --help`.",
			arg, strings.Join(r.knownSubcommands(), ", "), r.name())
	}
	if strings.HasPrefix(arg, "-") {
		return fmt.Sprintf("unknown flag %q — run `%s --help` for the supported flags.", arg, r.name())
	}
	return fmt.Sprintf("%q is neither a directory (a repository) nor an existing file (a config).\n\n"+
		"A path naming a DIRECTORY is treated as a repository; one naming a FILE is treated as a config.", arg)
}

// closestSubcommand returns the known subcommand within edit distance 2 of arg, if any —
// enough to catch a transposition or a doubled letter without guessing wildly.
func (r *Runner) closestSubcommand(arg string) string {
	best, bestDist := "", 3
	for _, cmd := range r.knownSubcommands() {
		if d := editDistance(strings.ToLower(arg), cmd); d < bestDist {
			best, bestDist = cmd, d
		}
	}
	return best
}

// editDistance is the standard Levenshtein distance over two short ASCII words.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// IsDirectory reports whether path names an existing directory. Used throughout to tell
// a repository argument from a config-file argument — the distinction that makes
// `--explain /path/to/repo` and `--explain cluster.yaml` both unambiguous.
func IsDirectory(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// FileExists reports whether path names an existing non-directory file.
func FileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// isDirectory / fileExists are the unexported spellings the moved command files use.
func isDirectory(path string) bool { return IsDirectory(path) }
func fileExists(path string) bool  { return FileExists(path) }
