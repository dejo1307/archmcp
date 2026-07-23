package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/enola-labs/enola/internal/config"
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

	if len(os.Args) > 1 && os.Args[1] == "upgrade" {
		if err := upgrade.Run(ctx, version.Version); err != nil {
			log.Fatalf("upgrade failed: %v", err)
		}
		os.Exit(0)
	}

	generateMode := false
	explainMode := false
	statusMode := false
	statusAll := false
	noDashboard := false
	cfgPath := "mcp-arch.yaml"
	explainRepo := "" // optional positional repo path for --explain

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
			// In --explain mode the positional argument is the repository path;
			// otherwise it is the config file path.
			if explainMode {
				explainRepo = arg
			} else {
				cfgPath = arg
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
		runExplain(ctx, eng, cfg, explainRepo)
		os.Exit(0)
	}

	if generateMode {
		repoPath, err := filepath.Abs(cfg.Repo)
		if err != nil {
			log.Fatalf("failed to resolve repo path: %v", err)
		}

		snapshot, err := eng.GenerateSnapshot(ctx, repoPath, false)
		if err != nil {
			log.Fatalf("snapshot generation failed: %v", err)
		}

		if err := eng.WriteArtifacts(repoPath); err != nil {
			log.Fatalf("failed to write artifacts: %v", err)
		}

		// Refresh the graph-wide receipt at ~/.enola/receipt.json. Non-fatal: a
		// failure here must not abort an otherwise-successful snapshot.
		if err := eng.WriteGlobalReceipt(); err != nil {
			log.Printf("warning: failed to write global receipt: %v", err)
		}

		fmt.Fprintf(os.Stderr, "\nSnapshot complete:\n")
		fmt.Fprintf(os.Stderr, "  Repository:  %s\n", snapshot.Meta.RepoPath)
		fmt.Fprintf(os.Stderr, "  Facts:       %d\n", snapshot.Meta.FactCount)
		fmt.Fprintf(os.Stderr, "  Insights:    %d\n", snapshot.Meta.InsightCount)
		fmt.Fprintf(os.Stderr, "  Artifacts:   %d\n", len(snapshot.Artifacts))
		fmt.Fprintf(os.Stderr, "  Duration:    %s\n", snapshot.Meta.Duration)
		fmt.Fprintf(os.Stderr, "  Output:      %s\n", filepath.Join(repoPath, cfg.Output.Dir))
		os.Exit(0)
	}

	bootstrap.AutoLoadSnapshot(eng, cfg)

	srv, err := bootstrap.NewServer(eng, cfg)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

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
	srv.SetToolCallback(tracker.OnToolCall)

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
	repo := repoArg
	if repo == "" {
		repo = cfg.Repo
	}
	repoPath, err := filepath.Abs(repo)
	if err != nil {
		log.Fatalf("failed to resolve repo path: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Analyzing %s …\n", repoPath)
	// --explain is a read-only, no-artifacts mode: reuse a cache if one exists,
	// but never write to .enola.
	eng.SetPersistCache(false)
	if _, err := eng.GenerateSnapshot(ctx, repoPath, false); err != nil {
		log.Fatalf("snapshot generation failed: %v", err)
	}

	report := explain.Compute(eng)
	fmt.Print(report.Render())
}
