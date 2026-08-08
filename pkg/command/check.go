package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/conformance"
	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/updatecheck"
	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/check"
)

// target is the repository the gate operates on, plus how it was resolved — reported to
// the user so which repo and which config were used is never a guess.
type target struct {
	engine     *bootstrap.Engine
	repoPaths  []string
	configNote string
}

// resolveTarget turns the single positional argument into an engine pointed at the right
// repository. The argument is a REPOSITORY when it names a directory and a CONFIG FILE
// when it names a file — the same disambiguation --explain uses, so `enola check
// /path/to/repo` means what a reader expects.
//
// Without this, a directory argument fell through to config.Load, which cannot read a
// directory: it warned and silently used built-in defaults, whose `repo: .` is the WORKING
// DIRECTORY. So `enola check /path/to/other/repo` analysed whichever repo you happened to
// be standing in and graded it against that repo's baseline — the wrong answer, presented
// as a real verdict.
//
// When the argument is a directory, a mcp-arch.yaml INSIDE it is picked up if present.
// That keeps `baseline pin` and `check` resolving identically, which matters more than it
// looks: the config determines the ignore globs, and a pin and a check that disagreed on
// them would differ in ignore-glob hash and make every diff decline as incomparable.
func (r *Runner) resolveTarget(arg string) target {
	cfgPath := "mcp-arch.yaml"
	repoOverride := ""

	switch {
	case arg == "":
	case isDirectory(arg):
		abs, err := filepath.Abs(arg)
		if err != nil {
			r.checkFatal("resolving repo path %q: %v", arg, err)
		}
		repoOverride = abs
		if inner := filepath.Join(abs, "mcp-arch.yaml"); fileExists(inner) {
			cfgPath = inner
		}
	default:
		cfgPath = arg
	}

	eng, cfg, err := r.newEngine(bootstrap.Options{ConfigPath: cfgPath})
	if err != nil {
		r.checkFatal("failed to create engine: %v", err)
	}

	// The note names the config that was LOADED, not the one that was looked for.
	// The two differ whenever the lookup falls back — to built-in defaults, or to a
	// config sitting beside the binary — and a note that reports the intent as
	// though it were the outcome is worse than none: it is a confirmation of
	// something nobody checked.
	note := "built-in default config"
	if cfg.SourcePath != "" {
		note = "config " + cfg.SourcePath
	}
	if repoOverride != "" {
		note = "repo " + repoOverride + " (" + note + ")"
	}
	if repoOverride != "" {
		// Repos would otherwise win in RepoPaths and silently ignore the directory the
		// caller named.
		cfg.Repo = repoOverride
		cfg.Repos = nil
	}
	repoPaths, err := cfg.RepoPaths()
	if err != nil {
		r.checkFatal("failed to resolve repo path: %v", err)
	}
	return target{engine: eng, repoPaths: repoPaths, configNote: note}
}

// runCheck is the `enola check` gate: snapshot the repo, diff it against a baseline,
// grade the delta, and exit with a code CI can act on.
//
// It never returns — it exits with the verdict's code. Exit codes are the contract:
// 0 clean, 1 regression, 2 usage/operational error, 3 declined (not comparable).
func (r *Runner) Check(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		baseline      = fs.String("baseline", "pinned", "what to compare against: pinned, previous, or a path to a directory holding facts.jsonl")
		failOn        = fs.String("fail-on", "", "comma-separated explainer names whose new findings fail (default: cycles)")
		minConfidence = fs.Float64("min-confidence", 0, "confidence floor within --fail-on explainers (default: 1.00)")
		warnOnly      = fs.Bool("warn-only", false, "report everything but always exit 0 on findings (blocking/usage errors still apply)")
		asJSON        = fs.Bool("json", false, "emit the verdict as JSON instead of text")
		focus         = fs.String("focus", "", "narrow the delta to entries referencing this module/file/symbol")
		detail        = fs.Bool("detail", false, "print the full delta (changed edges and facts) under the verdict")
		write         = fs.Bool("write", false, "persist snapshot artifacts to .enola/ (default: read-only, nothing is written)")
		target        = fs.String("target", "", "the symbol, type or package you INTENDED to change; packages reached outside its predicted blast radius are reported as spillover")
		expected      = fs.String("expected", "", "comma-separated packages you expected to touch, in addition to the predicted radius")
		maxSpillover  = fs.Int("max-spillover", -1, "fail when more than N packages are reached outside the declared scope (default: report only, never fail)")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: "+r.name()+" check [flags] [config_path]\n\n"+
			"Grade what a change did to the architecture, against a pinned baseline.\n\n"+
			"Exit codes:\n"+
			"  0  clean      no structural regression\n"+
			"  1  regression the policy was violated\n"+
			"  2  error      the gate could not run (no baseline, bad flag, inverted pair)\n"+
			"  3  declined   the baseline is not comparable; refusing to grade\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(check.StatusUsageError.ExitCode())
	}

	var arg string
	if rest := fs.Args(); len(rest) > 0 {
		arg = rest[0]
	}

	policy := check.Policy{
		MinConfidence: *minConfidence,
		WarnOnly:      *warnOnly,
	}
	// Spillover is REPORTED by default and fails only when a bound is given. A scope
	// check that broke builds the moment someone first passed --target would teach
	// people not to pass it.
	if *maxSpillover >= 0 {
		policy.Thresholds = append(policy.Thresholds, check.Threshold{
			Measurement: "spillover_packages",
			FailAt:      *maxSpillover + 1, // "allow up to N" — so N+1 is the failure
		})
	}
	if *failOn != "" {
		for _, name := range strings.Split(*failOn, ",") {
			if n := strings.TrimSpace(name); n != "" {
				policy.FailExplainers = append(policy.FailExplainers, n)
			}
		}
	}

	tgt := r.resolveTarget(arg)
	eng, repoPaths := tgt.engine, tgt.repoPaths
	fmt.Fprintf(os.Stderr, r.name()+" check: %s\n", tgt.configNote)

	// Read-only unless --write. Skipping WriteArtifacts is what makes it read-only:
	// that is where the snapshot is persisted AND where the previous/ rotation happens,
	// so a bare `enola check` leaves both the pinned baseline and previous/ untouched
	// and can be run repeatedly against the same baseline. SetPersistCache(false)
	// additionally keeps the extractor cache off disk, matching --explain.
	if !*write {
		eng.SetPersistCache(false)
	}
	for i, repoPath := range repoPaths {
		if _, err := eng.GenerateSnapshot(ctx, repoPath, i > 0); err != nil {
			r.checkFatal("snapshot generation failed for %s: %v", repoPath, err)
		}
	}
	if *write {
		for _, repoPath := range repoPaths {
			if err := eng.WriteArtifacts(repoPath); err != nil {
				r.checkFatal("failed to write artifacts for %s: %v", repoPath, err)
			}
		}
	}

	snap := eng.Snapshot()
	if snap == nil || eng.Store().Count() == 0 {
		r.checkFatal("snapshot produced no facts for %s", strings.Join(repoPaths, ", "))
	}
	// Build current from the store so it reflects the whole (possibly multi-repo) graph
	// rather than only the last repo indexed — the same construction diff_snapshot uses,
	// FactsRef included: diff.Compute reads its inputs and the published bundle is
	// immutable, so copying the fact set here would buy nothing.
	current := &facts.Snapshot{Meta: snap.Meta, Facts: eng.Store().FactsRef(), Insights: snap.Insights}

	// The baseline is anchored on the FIRST repo: that is the one whose snapshot reset
	// the graph, and it is where `enola baseline pin` writes.
	anchor := repoPaths[0]
	baseDir := engine.ResolveBaselineDir(eng.OutputDir(anchor), *baseline)
	base, err := bootstrap.LoadSnapshotDir(baseDir)
	if err != nil {
		r.checkFatal("%s", r.baselineHelp(*baseline, baseDir, anchor, err))
	}

	d := diff.Compute(base, current)
	if *focus != "" {
		d = d.Focused(*focus)
	}

	// Conformance: did the change stay inside what the caller declared? Computed only
	// when something WAS declared — a gate that graded scope nobody stated would be
	// grading its own guess.
	var measurements []check.Measurement
	var conf *conformance.Report
	if *target != "" || *expected != "" {
		baseStore := facts.NewStore()
		baseStore.Add(base.Facts...)
		rep := conformance.Compute(baseStore, eng.Store(), d, conformance.Options{
			Target:           *target,
			ExpectedPackages: splitList(*expected),
		})
		conf = &rep
		// Reported as a MEASUREMENT, not graded here. Whether spillover fails a build is
		// policy, and policy lives in one place — otherwise this gate and any other
		// surface computing the same number would come to disagree about it.
		measurements = append(measurements, check.Measurement{
			Name:  "spillover_packages",
			Label: "package(s) reached outside the declared scope",
			Count: len(rep.Spillover),
		})
	}

	verdict := check.Evaluate(d, policy, measurements...)

	if *asJSON {
		out, err := verdict.JSON()
		if err != nil {
			r.checkFatal("failed to encode verdict: %v", err)
		}
		fmt.Println(string(out))
	} else {
		if conf != nil {
			fmt.Print(conf.Render())
		}
		fmt.Print(verdict.Render())
		if *detail {
			fmt.Printf("\n%s\n", verdict.Detail())
		}
	}
	// After the verdict and on STDERR, in both output modes. Stderr because `--json`
	// promises stdout is a verdict document and nothing else, and after because a
	// housekeeping note must never be the first thing read when the gate just failed.
	updatecheck.Fprint(os.Stderr, engine.ExtractorVersion())
	os.Exit(verdict.ExitCode())
}

// splitList parses a comma-separated flag into a trimmed, non-empty list.
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// baselineHelp turns a failed baseline load into an actionable message rather than a
// bare stat error — the most common way a first run of `enola check` fails is simply
// that no baseline was ever pinned.
// repoHint is the repository the caller named, echoed back into the suggested commands so
// they can be pasted as-is rather than adapted.
func (r *Runner) baselineHelp(selector, dir, repoHint string, err error) string {
	switch strings.ToLower(strings.TrimSpace(selector)) {
	case "", "pinned":
		return fmt.Sprintf("no pinned baseline at %s\n\n"+
			"The gate needs a \"before\" to compare against, pinned BEFORE you edit:\n"+
			"    "+r.name()+" baseline pin %s\n"+
			"    …make your change…\n"+
			"    "+r.name()+" check %s\n\n"+
			"Or compare against the immediately-preceding snapshot with --baseline=previous.",
			dir, repoHint, repoHint)
	case "previous":
		return fmt.Sprintf("no previous snapshot at %s — that requires at least two snapshots "+
			"written with `"+r.name()+" --generate` (note `"+r.name()+" check` is read-only by default and does "+
			"not rotate it).\n\nFor a stable baseline across several rounds of edits, prefer:\n"+
			"    "+r.name()+" baseline pin %s", dir, repoHint)
	default:
		return fmt.Sprintf("could not load baseline from %q: %v", dir, err)
	}
}

// checkFatal reports an operational failure and exits 2 — distinct from a regression,
// because the gate did not run at all.
func (r *Runner) checkFatal(format string, args ...any) { r.cmdFatal("check", format, args...) }

// cmdFatal reports an operational failure and exits 2 — distinct from a regression,
// because the command did not run at all. The command name is a parameter so a shared
// helper cannot misattribute an error to the wrong subcommand.
func (r *Runner) cmdFatal(cmd, format string, args ...any) {
	fmt.Fprintf(os.Stderr, r.name()+" "+cmd+": "+format+"\n", args...)
	os.Exit(check.StatusUsageError.ExitCode())
}

// runBaseline is `enola baseline pin|show|clear` — the CLI half of the loop, so the
// baseline can be managed without an agent.
func (r *Runner) Baseline(args []string) {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: "+r.name()+" baseline <pin|show|clear> [config_path]\n\n"+
			"  pin    freeze the current .enola snapshot as the diff baseline\n"+
			"  show   report whether a baseline exists, and what it describes\n"+
			"  clear  remove the pinned baseline\n")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(check.StatusUsageError.ExitCode())
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		os.Exit(check.StatusUsageError.ExitCode())
	}
	action := strings.ToLower(rest[0])

	var arg string
	if len(rest) > 1 {
		arg = rest[1]
	}
	tgt := r.resolveTarget(arg)
	eng := tgt.engine
	anchor := tgt.repoPaths[0]
	outDir := eng.OutputDir(anchor)
	baseDir := engine.ResolveBaselineDir(outDir, "pinned")

	switch action {
	case "pin":
		// Snapshot first, then pin. "Pin a baseline of this repo" is one intent, and
		// requiring a separate --generate made the first useful thing a new user does a
		// two-command ritual whose failure mode ("no snapshot to pin") explained the
		// mechanism rather than the goal.
		//
		// Always regenerating is also the safer semantic: pinning whatever happened to be
		// on disk could freeze a snapshot from days ago as "the state before my change",
		// which is precisely the staleness the diff then warns about. Snapshots are
		// deterministic, so for an unchanged tree this costs a cached re-index and
		// produces byte-identical facts.
		fmt.Fprintf(os.Stderr, r.name()+" baseline: %s\n", tgt.configNote)
		for i, repoPath := range tgt.repoPaths {
			if _, err := eng.GenerateSnapshot(context.Background(), repoPath, i > 0); err != nil {
				r.checkFatal("snapshot generation failed for %s: %v", repoPath, err)
			}
		}
		for _, repoPath := range tgt.repoPaths {
			if err := eng.WriteArtifacts(repoPath); err != nil {
				r.checkFatal("failed to write artifacts for %s: %v", repoPath, err)
			}
		}
		if err := eng.SetBaseline(anchor); err != nil {
			r.checkFatal("could not pin baseline: %v", err)
		}
		fmt.Printf("Baseline pinned for %s\n", anchor)
		if snap, err := bootstrap.LoadSnapshotDir(baseDir); err == nil {
			describeBaseline(snap)
		}
		fmt.Printf("\nNow make your changes, then run:\n    %s check %s\n", r.name(), anchor)
	case "show":
		snap, err := bootstrap.LoadSnapshotDir(baseDir)
		if err != nil {
			fmt.Printf("No baseline pinned (%s does not hold a snapshot).\nRun `%s --generate` then `%s baseline pin`.\n", baseDir, r.name(), r.name())
			os.Exit(check.StatusUsageError.ExitCode())
		}
		fmt.Printf("Baseline at %s\n", baseDir)
		describeBaseline(snap)
	case "clear":
		if err := os.RemoveAll(baseDir); err != nil {
			r.checkFatal("could not clear baseline: %v", err)
		}
		fmt.Printf("Baseline cleared (%s removed).\n", baseDir)
	default:
		fmt.Fprintf(os.Stderr, r.name()+" baseline: unknown action %q\n", action)
		fs.Usage()
		os.Exit(check.StatusUsageError.ExitCode())
	}
}

// describeBaseline prints what a pinned baseline actually describes, so `show` answers
// "is this still the right thing to compare against?" rather than only "it exists".
func describeBaseline(snap *facts.Snapshot) {
	m := snap.Meta
	fmt.Printf("  Generated: %s\n", orUnknown(m.GeneratedAt))
	fmt.Printf("  Repo:      %s\n", orUnknown(m.RepoPath))
	if r := m.Receipt(); r.Git != nil {
		state := "clean"
		if r.Git.Dirty {
			state = "dirty (uncommitted changes)"
		}
		fmt.Printf("  Git:       %s @ %s — %s\n", orUnknown(r.Git.Ref), shortCommit(r.Git.Commit), state)
		// The repository identity a restored baseline is matched on. Shown because
		// "which repo does this baseline describe?" is the question `show` exists to
		// answer, and after an import the answer is no longer obvious from the path.
		if r.Git.Remote != "" {
			fmt.Printf("  Remote:    %s\n", r.Git.Remote)
		}
	}
	fmt.Printf("  Facts:     %d · Insights: %d\n", len(snap.Facts), len(snap.Insights))
	fmt.Printf("  Snapshot:  %s\n", orUnknown(m.Receipt().SnapshotID))
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unknown)"
	}
	return s
}

func shortCommit(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return orUnknown(s)
}
