package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/enola-labs/enola/pkg/coverage"
)

// runCoverage is `enola coverage`: which cross-repo edges enola resolved, and which it
// did not.
//
// It exists because the report was previously reachable only through an MCP tool call —
// so an agent could ask, and the person deciding whether to trust enola's cross-repo
// linking could not. Cross-repo resolution is the claim hardest to verify from outside,
// and the only convincing way to settle it is to let someone run it against code they
// already know.
//
// A report, not a gate: it exits 0 whenever it ran. `enola check` owns the meaning of a
// non-zero exit — "your change did something" — and blurring that here would cost more
// than a threshold flag is worth.
func runCoverage(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		service    = fs.String("repo", "", "report on one service instead of all of them")
		unresolved = fs.Bool("unresolved", false, "list only services with unresolved outbound call sites")
		asJSON     = fs.Bool("json", false, "emit the report as JSON")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr,
			"Usage: enola coverage [flags] [repo_path|config_path]\n\n"+
				"Report which cross-repo edges enola resolved and which it could not, per service.\n"+
				"Tells a genuinely isolated service apart from one whose outbound edges enola simply\n"+
				"failed to follow — a distinction the graph alone cannot express.\n\n"+
				"Needs two or more repositories in one graph; point it at a config with `repos:`.\n\n"+
				"Flags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	var arg string
	if rest := fs.Args(); len(rest) > 0 {
		arg = rest[0]
	}
	tgt := resolveTarget(arg)
	fmt.Fprintf(os.Stderr, "enola coverage: %s\n", tgt.configNote)

	// Read-only, like `check` and `--explain`: reporting on a graph must not rewrite it.
	tgt.engine.SetPersistCache(false)
	for i, repoPath := range tgt.repoPaths {
		if _, err := tgt.engine.GenerateSnapshot(ctx, repoPath, i > 0); err != nil {
			coverageFatal("snapshot generation failed for %s: %v", repoPath, err)
		}
	}

	report := coverage.Build(tgt.engine.Store(), *service)
	if *service != "" && len(report) == 0 {
		coverageFatal("no service named %q in this graph", *service)
	}
	if *unresolved {
		report = onlyUnresolved(report)
	}

	if *asJSON {
		out, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			coverageFatal("failed to encode report: %v", err)
		}
		fmt.Println(string(out))
		return
	}
	fmt.Print(report.RenderText())
}

func coverageFatal(format string, args ...any) { cmdFatal("coverage", format, args...) }

// onlyUnresolved narrows the report to services with unresolved call sites — the
// failure-hunting view, for someone working through blind spots rather than surveying.
func onlyUnresolved(r coverage.Report) coverage.Report {
	var out coverage.Report
	for _, s := range r {
		if s.UnresolvedTotal > 0 {
			out = append(out, s)
		}
	}
	return out
}
