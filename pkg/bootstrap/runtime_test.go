package bootstrap

import (
	"bytes"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"testing"
)

// The reason this file exists: ConfigureRuntime used to announce itself on stderr, so
// every invocation of every command — `--version`, `--help`, `enola log`, a two-line
// report — opened with a line about GC tuning. A default that is working correctly is not
// news, and printing it on every run teaches the eye to skip the first line of enola's
// output, which is exactly where its warnings appear.
func TestConfigureRuntime_IsSilent(t *testing.T) {
	restore := captureLog(t)
	ConfigureRuntime()
	if out := restore(); out != "" {
		t.Errorf("ConfigureRuntime must not log; got:\n%s", out)
	}
}

// Silent must not mean absent: the limit is still applied, because the reason it exists
// (a kernel-sized graph pushing the process into the OOM-killer) did not go away.
func TestConfigureRuntime_StillAppliesTheLimit(t *testing.T) {
	// ConfigureRuntime branches on LookupEnv, and an empty t.Setenv value still reads as
	// SET — so the variable has to be genuinely removed for this to exercise the
	// auto-detect path.
	unsetGoMemLimit(t)

	before := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(before) })

	debug.SetMemoryLimit(noMemLimit)
	ConfigureRuntime()

	if got := debug.SetMemoryLimit(-1); got == noMemLimit {
		t.Error("no soft memory limit was applied")
	}
}

// An operator's explicit choice wins, and the report says so rather than presenting it as
// enola's own default — the difference matters when somebody is working out why a limit
// is where it is.
func TestMemoryLimit_NamesTheSource(t *testing.T) {
	before := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(before) })

	t.Setenv("GOMEMLIMIT", "42MiB")
	debug.SetMemoryLimit(42 << 20)
	_, source := MemoryLimit()
	if !strings.Contains(source, "GOMEMLIMIT") {
		t.Errorf("an explicit override must be named as one, got %q", source)
	}
	if line := MemoryLimitLine(); !strings.Contains(line, "42 MiB") {
		t.Errorf("line = %q, want the limit in MiB", line)
	}
}

// A platform where system memory cannot be detected leaves no limit at all. Reported as
// such rather than as a very large number, which reads like a limit somebody chose.
func TestMemoryLimitLine_NoLimit(t *testing.T) {
	before := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(before) })

	unsetGoMemLimit(t)
	debug.SetMemoryLimit(noMemLimit)

	if line := MemoryLimitLine(); !strings.Contains(line, "no soft memory limit") {
		t.Errorf("line = %q, want it to say there is no limit", line)
	}
}

// unsetGoMemLimit removes GOMEMLIMIT for the duration of a test, restoring whatever was
// there. t.Setenv cannot express this: an empty value is still a SET variable, and
// ConfigureRuntime branches on LookupEnv.
func unsetGoMemLimit(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv("GOMEMLIMIT")
	if err := os.Unsetenv("GOMEMLIMIT"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("GOMEMLIMIT", prev)
		}
	})
}

// captureLog redirects the standard logger and returns a function yielding what was
// written and restoring the previous output.
func captureLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	return func() string {
		log.SetOutput(prev)
		return buf.String()
	}
}
