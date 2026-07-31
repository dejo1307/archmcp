package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/hookstate"
	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/check"
)

// runDoctor is `enola doctor`: does the loop actually run in this repository?
//
// It exists because of a failure mode a test cannot cover. `enola install --hooks`
// wrote a configuration the agent silently ignored, so the hooks never fired — and
// nothing anywhere said so. The installer reported success, the hook binary worked when
// invoked by hand, and the agent simply never called it.
//
// The shape of that configuration is a contract owned by the agent, which ships on its
// own schedule and can change after enola is released. So the durable check is not a
// test in this repository — it is asking, on the machine where it matters, whether the
// hooks have fired lately. That is all this command does.
//
// Exit codes follow `enola coverage`, not `enola check`: this is a report, so it exits
// 0 whenever it ran, and 2 only for a usage error. A non-zero code from enola means
// "your change did something", and a diagnostic must not borrow that meaning.
func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr,
			"Usage: enola doctor [flags] [repo_path]\n\n"+
				"Report whether enola's agent hooks are actually firing in this repository.\n\n"+
				"`install --hooks` can write a configuration the agent ignores — it reports\n"+
				"success either way. This asks the only question that settles it: when did the\n"+
				"hooks last run?\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	repoDir, err := os.Getwd()
	if err != nil {
		cmdFatal("doctor", "cannot determine the working directory: %v", err)
	}
	if rest := fs.Args(); len(rest) > 0 {
		if !isDirectory(rest[0]) {
			cmdFatal("doctor", "%q is not a directory", rest[0])
		}
		repoDir = rest[0]
	}
	abs, err := filepath.Abs(repoDir)
	if err == nil {
		repoDir = abs
	}

	outDir := hookOutputDir(repoDir)
	state := hookstate.Load(outDir)
	installed := hooksConfigured(repoDir)
	baselineIssue := baselineUsability(repoDir, outDir)

	if *asJSON {
		out := map[string]any{
			"repo":              repoDir,
			"hooks_configured":  installed,
			"state":             state,
			"baseline_unusable": baselineIssue,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}

	fmt.Printf("enola doctor: %s\n\n", repoDir)

	// Asked and answered BEFORE a session ends, which is the difference between this
	// and reading the last outcome below: an unusable baseline makes the hooks decline
	// to grade, and they decline quietly. Knowing now beats finding out afterwards.
	fmt.Println("Baseline")
	if baselineIssue == "" {
		fmt.Println("  comparable — the gate can grade against it")
	} else {
		fmt.Printf("  NOT COMPARABLE: %s\n", baselineIssue)
		fmt.Println("  Nothing will be graded against it until it is re-pinned:")
		fmt.Println("      enola baseline pin")
	}
	fmt.Println()

	fmt.Println("Session hooks")

	if !installed {
		fmt.Println("  not configured for this repository.")
		fmt.Println("  Run `enola install --hooks` to have the loop run itself: a baseline pinned")
		fmt.Println("  at session start, and the change graded when the session ends.")
		return
	}
	fmt.Println("  configured in .claude/settings.json")
	if !state.InstalledAt.IsZero() {
		fmt.Printf("  installed        %s (%s ago)\n",
			state.InstalledAt.Format(time.RFC3339), humanSince(state.InstalledAt))
	}
	if state.HookCommand != "" {
		fmt.Printf("  hook command     %s\n", state.HookCommand)
	}
	fmt.Println()

	for _, e := range []struct {
		event hookstate.Event
		label string
		does  string
	}{
		{hookstate.EventSessionStart, "session-start", "pins the baseline your change is graded against"},
		{hookstate.EventStop, "stop", "grades the change when the session ends"},
	} {
		r := state.Get(e.event)
		fmt.Printf("  %-15s %s\n", e.label, e.does)
		if r == nil || r.Count == 0 {
			fmt.Printf("  %-15s NEVER FIRED\n", "")
			continue
		}
		fmt.Printf("  %-15s last ran %s ago · %d run(s) · last outcome: %s\n",
			"", humanSince(r.LastFired), r.Count, r.LastOutcome)
	}

	// The verdict, stated rather than left for the reader to assemble.
	fmt.Println()
	switch {
	case !state.Fired(hookstate.EventStop) && !state.Fired(hookstate.EventSessionStart):
		fmt.Println("  NEITHER HOOK HAS EVER RUN.")
		fmt.Println("  The configuration exists but nothing is invoking it. Start a session in this")
		fmt.Println("  repository and re-run; if it still says this, the agent is not reading the")
		fmt.Println("  hooks — a shape enola writes that your agent version ignores would look")
		fmt.Println("  exactly like this. Please report it.")
	case !state.Fired(hookstate.EventStop):
		fmt.Println("  THE STOP HOOK HAS NEVER RUN — the half that reports regressions.")
		fmt.Println("  A baseline is being pinned and nothing is grading against it.")
	case state.Get(hookstate.EventStop).LastOutcome == hookstate.OutcomeDeclined:
		fmt.Println("  The stop hook is running, but last declined to grade: the baseline was not")
		fmt.Println("  comparable to the current snapshot. It stays silent in-session when this")
		fmt.Println("  happens, so it looks identical to a clean run. Re-pin with")
		fmt.Println("  `enola baseline pin` and check `enola check` for the reason.")
	case state.Get(hookstate.EventStop).LastOutcome == hookstate.OutcomeUnavailable:
		fmt.Println("  The stop hook is running but has nothing to grade against — usually no")
		fmt.Println("  baseline yet. `enola baseline pin` fixes it; the session-start hook will")
		fmt.Println("  also do it on the next session.")
	default:
		fmt.Println("  Both hooks are firing. The loop is closed.")
	}
}

// baselineUsability returns why the pinned baseline could not be graded against, or
// "" when it can. It answers from metadata alone — no files are parsed — so `doctor`
// stays a report rather than becoming a snapshot.
func baselineUsability(repoDir, outDir string) string {
	base, err := bootstrap.LoadSnapshotDir(engine.ResolveBaselineDir(outDir, "pinned"))
	if err != nil {
		return "" // no baseline pinned; the hooks section covers that case
	}
	eng, cfg, err := bootstrap.NewEngine(bootstrap.Options{ConfigPath: configForRepo(repoDir)})
	if err != nil {
		return ""
	}
	cfg.Repo, cfg.Repos = repoDir, nil
	current := eng.CurrentMeta(repoDir)
	if current == nil {
		return ""
	}
	v := check.Evaluate(&diff.SnapshotDiff{Comparability: diff.CompareMeta(base.Meta, *current)}, check.Policy{})
	return v.DeclineReason()
}

// hooksConfigured reports whether .claude/settings.json carries an entry enola owns.
//
// Deliberately a shallow scan for the marker rather than a shape check: whether the
// shape is one the agent honours is exactly the question this command cannot answer
// from the file, and pretending otherwise is how the original defect survived. The
// heartbeat answers it; this only establishes that something was written.
func hooksConfigured(repoDir string) bool {
	data, err := os.ReadFile(filepath.Join(repoDir, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	var doc struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return false
	}
	for _, raw := range doc.Hooks {
		if containsEnolaMarker(raw) {
			return true
		}
	}
	return false
}

// containsEnolaMarker walks arbitrary JSON looking for enola's ownership marker, so it
// keeps working if the surrounding structure changes shape again.
func containsEnolaMarker(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	var walk func(any) bool
	walk = func(n any) bool {
		switch t := n.(type) {
		case map[string]any:
			if s, ok := t["source"].(string); ok && s == "enola" {
				return true
			}
			for _, child := range t {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range t {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

// humanSince renders an age at the resolution a human cares about here — the question
// is "recently, or never", not "how many milliseconds".
func humanSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minute(s)", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hour(s)", int(d.Hours()))
	default:
		return fmt.Sprintf("%d day(s)", int(d.Hours()/24))
	}
}
