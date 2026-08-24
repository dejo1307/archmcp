package main

import (
	"fmt"
	"io"
)

// printDashboardHint points at the dashboard after an action that persisted
// snapshot artifacts, so the reader always knows how to open the visual view
// of what was just written. It is unconditional — unlike an interactive
// prompt, a one-line hint costs nothing when stdout/stderr are redirected,
// which is the common case for --generate run from scripts and CI.
func printDashboardHint(out io.Writer, repoArg, cfgPath string) {
	_, _ = fmt.Fprintln(out, "\nExplore this snapshot in your browser:")
	if target := dashboardHintTarget(repoArg, cfgPath); target != "" {
		_, _ = fmt.Fprintf(out, "  enola dashboard --open %q\n", target)
	} else {
		_, _ = fmt.Fprintln(out, "  enola dashboard --open")
	}
	_, _ = fmt.Fprintln(out, "It starts in the background; stop it later with: enola dashboard stop")
}

// dashboardHintTarget names the repo or config path a subsequent `dashboard`
// invocation should be pointed at, mirroring how the action itself resolved
// its target. Empty means the default target already matches.
func dashboardHintTarget(repoArg, cfgPath string) string {
	if repoArg != "" {
		return repoArg
	}
	if cfgPath != "" && cfgPath != "mcp-arch.yaml" {
		return cfgPath
	}
	return ""
}
