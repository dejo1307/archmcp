package cli

import (
	"os"
)

// ShowDashboardHint reports whether a one-line dashboard discovery hint belongs in
// this process's output. Hints are useful in a terminal but are noise in CI logs,
// redirected scripts, and agent automation. Hook subcommands never call this helper;
// the environment and terminal checks keep the boundary explicit for other callers.
func ShowDashboardHint(out *os.File) bool {
	return shouldShowDashboardHint(os.Getenv("CI"), os.Getenv("ENOLA_NO_PROMPTS"), isTerminal(out))
}

func shouldShowDashboardHint(ci, noPrompts string, outputTTY bool) bool {
	return ci == "" && noPrompts == "" && outputTTY
}

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
