package main

import (
	"fmt"
	"io"

	"github.com/enola-labs/enola/pkg/cli"
)

// printDashboardHint points at the dashboard after an action that persisted
// snapshot artifacts, so the reader always knows how to open the visual view
// of what was just written. The wording itself lives in cli.DashboardHint,
// shared with pkg/command/check.go's --write path, so the two cannot drift.
func printDashboardHint(out io.Writer, repoArg, cfgPath string) {
	_, _ = fmt.Fprint(out, cli.DashboardHint("enola", dashboardHintTarget(repoArg, cfgPath)))
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
