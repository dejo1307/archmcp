package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/upgrade"
	"github.com/enola-labs/enola/internal/version"
	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/cli"
	"github.com/enola-labs/enola/pkg/dashboard"
	"github.com/enola-labs/enola/pkg/explain"
	"github.com/enola-labs/enola/pkg/status"
)

func main() {
	log.SetOutput(os.Stderr)
	bootstrap.ConfigureRuntime()

	// A terminated server must still tidy up after itself — remove its instance
	// record and flush its counters — so cancel the server's context on a signal
	// instead of dying mid-run. Run returns, and the deferred cleanup happens.
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// Subcommands are dispatched before the flag loop below, which is an exact-match
	// switch over os.Args and cannot parse `--flag=value`. Each subcommand owns its own
	// FlagSet instead.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "upgrade":
			if err := upgrade.Run(ctx, version.Version); err != nil {
				log.Fatalf("upgrade failed: %v", err)
			}
			os.Exit(0)
		case "check":
			runCheck(ctx, os.Args[2:]) // exits with the verdict's code
		case "coverage":
			runCoverage(ctx, os.Args[2:])
			os.Exit(0)
		case "baseline":
			runBaseline(os.Args[2:])
			os.Exit(0)
		case "hook":
			runHook(ctx, os.Args[2:]) // always exits 0; never disturbs a session
		case "install":
			runInstall(os.Args[2:], false)
			os.Exit(0)
		case "uninstall":
			runInstall(os.Args[2:], true)
			os.Exit(0)
		}
	}

	generateMode := false
	explainMode := false
	statusMode := false
	statusAll := false
	noDashboard := false
	cfgPath := "mcp-arch.yaml"
	repoArg := "" // optional positional repo path, for --explain and --generate

	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version":
			fmt.Fprintf(os.Stderr, "enola version %s\n", version.Version)
			os.Exit(0)
		case "--help", "-h":
			cli.RenderHelp(os.Stderr, helpSpec())
			os.Exit(0)
		case "--list":
			fmt.Fprint(os.Stderr, cli.RenderToolList(cli.ToolListSpec{}))
			os.Exit(0)
		case "--generate":
			generateMode = true
		case "--explain":
			explainMode = true
		case "--status":
			statusMode = true
		case "--all":
			statusAll = true
		case "--no-dashboard":
			noDashboard = true
		default:
			// The positional argument is a REPOSITORY when it names a directory and a
			// CONFIG FILE when it names a file, so both `--explain /path/to/repo` and
			// `--explain cluster.yaml` are unambiguous — without the distinction the
			// latter would be analysed as if the YAML file were a repository.
			//
			// The same applies to --generate: pointing it at a directory is the obvious
			// way to snapshot another repo, and treating that as a config path made it
			// fall back to defaults (repo: ".") and silently snapshot the working
			// directory instead of the repository named.
			//
			// Anything else is REJECTED rather than passed on as a config path. An
			// unreadable config is only a warning inside config.Load — by design, so a
			// missing mcp-arch.yaml falls back to defaults — but combined with a
			// catch-all default: here, every typo inherited that leniency and became a
			// silent wrong action. `enola chekc .` started an MCP server; `enola
			// --generate /does/not/exist.yaml` snapshotted the working directory. Both
			// looked like they worked.
			switch {
			case isDirectory(arg):
				repoArg = arg
			case fileExists(arg):
				cfgPath = arg
			default:
				log.Fatalf("%s", unknownArgHelp(arg))
			}
		}
	}

	// --status reads only the recorded usage under ~/.enola/usage/, so it runs
	// before the engine is built and never touches the repo.
	if statusMode {
		if statusAll {
			status.PrintStatusAll()
		} else {
			status.PrintStatus()
		}
		os.Exit(0)
	}

	eng, cfg, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: cfgPath,
	})
	if err != nil {
		log.Fatalf("failed to create engine: %v", err)
	}

	if explainMode {
		runExplain(ctx, eng, cfg, repoArg)
		os.Exit(0)
	}

	if generateMode {
		// A positional directory names one repository and overrides the config, so
		// `enola --generate /path/to/repo` snapshots that repo rather than the working
		// directory. Repos must be cleared too, or it would win in RepoPaths.
		if repoArg != "" {
			abs, err := filepath.Abs(repoArg)
			if err != nil {
				log.Fatalf("failed to resolve repo path: %v", err)
			}
			cfg.Repo, cfg.Repos = abs, nil
		}
		repoPaths, err := cfg.RepoPaths()
		if err != nil {
			log.Fatalf("failed to resolve repo path: %v", err)
		}

		var snapshot *facts.Snapshot
		for i, repoPath := range repoPaths {
			// The first repository resets the store; the rest append to it, which is
			// what makes one process produce one linked graph.
			snapshot, err = eng.GenerateSnapshot(ctx, repoPath, i > 0)
			if err != nil {
				log.Fatalf("snapshot generation failed for %s: %v", repoPath, err)
			}
			// In multi-repo mode WriteArtifacts writes the whole store to each
			// repo's output dir, matching what the MCP server does per generate.
			if err := eng.WriteArtifacts(repoPath); err != nil {
				log.Fatalf("failed to write artifacts for %s: %v", repoPath, err)
			}
		}

		// Refresh the graph-wide receipt at ~/.enola/receipt.json. Non-fatal: a
		// failure here must not abort an otherwise-successful snapshot.
		if err := eng.WriteGlobalReceipt(); err != nil {
			log.Printf("warning: failed to write global receipt: %v", err)
		}

		fmt.Fprintf(os.Stderr, "\nSnapshot complete:\n")
		if len(repoPaths) > 1 {
			fmt.Fprintf(os.Stderr, "  Repositories: %d\n", len(repoPaths))
			for _, p := range repoPaths {
				fmt.Fprintf(os.Stderr, "    - %s\n", p)
			}
		} else {
			fmt.Fprintf(os.Stderr, "  Repository:  %s\n", snapshot.Meta.RepoPath)
		}
		fmt.Fprintf(os.Stderr, "  Facts:       %d\n", snapshot.Meta.FactCount)
		fmt.Fprintf(os.Stderr, "  Insights:    %d\n", snapshot.Meta.InsightCount)
		fmt.Fprintf(os.Stderr, "  Artifacts:   %d\n", len(snapshot.Artifacts))
		fmt.Fprintf(os.Stderr, "  Duration:    %s\n", snapshot.Meta.Duration)
		fmt.Fprintf(os.Stderr, "  Output:      %s\n", filepath.Join(repoPaths[len(repoPaths)-1], cfg.Output.Dir))
		os.Exit(0)
	}

	restoredCorpus := bootstrap.AutoLoadSnapshot(eng, cfg)

	srv, err := bootstrap.NewServer(eng, cfg)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	// Size the restored graph before serving, so queries against it are priced
	// correctly without waiting for a snapshot this process does not need.
	srv.SeedCorpus(restoredCorpus)

	// Record per-tool usage so a later `enola --status` has something to report.
	// Per-repo counters are loaded lazily from ~/.enola/usage/ on first touch, so
	// they survive restarts; the config's repo is the fallback for calls made
	// before any snapshot is loaded. srv.StartTime() is only set once Run() is
	// called, so stamp the start time here to get a correct uptime.
	repoPath, _ := filepath.Abs(cfg.Repo)
	tracker := status.NewTracker(repoPath)
	tracker.SetStartTime(time.Now())
	// Identity + graph state make this process distinguishable in the instance
	// registry — a user typically runs one server per agent terminal, and every
	// dashboard and --status listing is built from these records.
	wd, _ := os.Getwd()
	tracker.SetIdentity(status.Identity{
		Binary:     "enola",
		Version:    version.Version,
		ConfigPath: cfgPath,
		WorkDir:    wd,
	})
	tracker.SetGraphFunc(bootstrap.GraphStateFunc(eng))
	// Deregister on the way out so the registry reflects the shutdown at once
	// rather than waiting for a reader to reap a dead PID.
	defer tracker.Close()
	srv.SetToolCallback(tracker.Record)

	// Start the localhost HTTP dashboard alongside the MCP server. It binds a
	// free loopback port — plus the shared, bookmarkable one if it can claim it —
	// and serves a read-only, auto-refreshing view of THIS server's graph and
	// activity, with links to every other server running. Non-fatal: a dashboard
	// failure must never stop the MCP server. The port is recorded on the tracker
	// BEFORE the startup write, so a separate --status invocation can print the
	// URL even before the first tool call.
	if !noDashboard {
		opts := dashboard.Options{
			Tracker:    tracker,
			StablePort: dashboard.ResolveStablePort(cfg.Dashboard.Port),
		}
		if dash, err := dashboard.Start(eng, opts); err != nil {
			log.Printf("dashboard: not started: %v (continuing without it)", err)
		} else {
			tracker.SetDashboardPort(dash.Port())
			fmt.Fprintf(os.Stderr, "Dashboard: %s (auto-refreshes every 30s)\n", dash.URL())
			if opts.StablePort > 0 {
				fmt.Fprintf(os.Stderr, "Shared URL: http://127.0.0.1:%d (whichever server holds it lists all the others)\n", opts.StablePort)
			}
		}
	}
	tracker.PersistStartup()

	if err := srv.Run(ctx); err != nil {
		// Deregister explicitly: log.Fatalf exits without running deferred calls.
		tracker.Close()
		log.Fatalf("server error: %v", err)
	}
}

// helpSpec is the `--help` text for this binary: the shared engine help with
// the OSS-only `upgrade` command documented on top.
func helpSpec() cli.HelpSpec {
	spec := cli.DefaultHelp(cli.Binary{
		Name:       "enola",
		CmdPackage: "./cmd/enola",
		VersionVar: "github.com/enola-labs/enola/internal/version.Version",
	})
	spec.Usage = append(spec.Usage, "enola upgrade")
	spec.Commands = append(spec.Commands, cli.FlagDoc{
		Flag: "upgrade",
		Desc: "Download and install the latest enola release, replacing the\nrunning binary in place.",
	})
	return spec
}

// runExplain indexes the given repository (defaulting to the configured repo)
// and prints a human-readable statistical summary to stdout.
func runExplain(ctx context.Context, eng *bootstrap.Engine, cfg *config.Config, repoArg string) {
	// A positional argument names one repository and overrides the config; with no
	// argument the config decides, which is what lets `repos:` produce a report over
	// the whole cluster rather than its first member.
	var repoPaths []string
	if repoArg != "" {
		abs, err := filepath.Abs(repoArg)
		if err != nil {
			log.Fatalf("failed to resolve repo path: %v", err)
		}
		repoPaths = []string{abs}
	} else {
		var err error
		if repoPaths, err = cfg.RepoPaths(); err != nil {
			log.Fatalf("failed to resolve repo path: %v", err)
		}
	}

	// --explain is a read-only, no-artifacts mode: reuse a cache if one exists,
	// but never write to .enola.
	eng.SetPersistCache(false)
	for i, repoPath := range repoPaths {
		fmt.Fprintf(os.Stderr, "Analyzing %s …\n", repoPath)
		if _, err := eng.GenerateSnapshot(ctx, repoPath, i > 0); err != nil {
			log.Fatalf("snapshot generation failed for %s: %v", repoPath, err)
		}
	}

	report := explain.Compute(eng)
	fmt.Print(report.Render())
}

// knownSubcommands are dispatched before the flag loop. Listed here so an argument that
// was MEANT to be one gets a targeted message instead of a generic path error.
var knownSubcommands = []string{"check", "coverage", "baseline", "install", "uninstall", "upgrade"}

// unknownArgHelp explains a rejected argument in terms of what the caller probably meant.
//
// The two cases worth separating are a mistyped subcommand and a bad path: telling someone
// who typed `enola chekc .` that "no such file or directory" would send them looking at
// their filesystem rather than at their spelling.
func unknownArgHelp(arg string) string {
	if !strings.HasPrefix(arg, "-") && !strings.ContainsAny(arg, "/\\.") {
		if near := closestSubcommand(arg); near != "" {
			return fmt.Sprintf("unknown command %q — did you mean %q?\n\n    enola %s --help\n\nRun `enola --help` for all commands.", arg, near, near)
		}
		return fmt.Sprintf("unknown command %q (expected one of: %s), and it is not a path either.\n\nRun `enola --help`.",
			arg, strings.Join(knownSubcommands, ", "))
	}
	if strings.HasPrefix(arg, "-") {
		return fmt.Sprintf("unknown flag %q — run `enola --help` for the supported flags.", arg)
	}
	return fmt.Sprintf("%q is neither a directory (a repository) nor an existing file (a config).\n\n"+
		"A path naming a DIRECTORY is treated as a repository; one naming a FILE is treated as a config.", arg)
}

// closestSubcommand returns the known subcommand within edit distance 2 of arg, if any —
// enough to catch a transposition or a doubled letter without guessing wildly.
func closestSubcommand(arg string) string {
	best, bestDist := "", 3
	for _, cmd := range knownSubcommands {
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

// isDirectory reports whether path names an existing directory. Used to tell a
// repository argument from a config-file argument.
func isDirectory(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
