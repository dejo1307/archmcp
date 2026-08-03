package bootstrap

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"

	"github.com/pbnjay/memory"
)

// memLimitFraction is the share of total system RAM used as the Go soft memory
// limit when one is not configured explicitly. It is deliberately high: the
// limit should only engage near the real ceiling so normal-sized repos run with
// the default GC, while a kernel-sized load is pushed into more aggressive GC
// (trading CPU) instead of being OOM-killed. Setting it much lower risks a GC
// death-spiral, which is worse for a long-running server than a clean exit.
const memLimitFraction = 0.90

// noMemLimit is what the Go runtime reports when no soft limit is in force.
const noMemLimit = int64(math.MaxInt64)

// ConfigureRuntime applies process-wide runtime settings that keep enola
// well-behaved when a single large repository (e.g. the Linux kernel) is loaded
// into the in-memory fact store. It is safe to call once at startup from a
// binary's main(); library callers of NewEngine are intentionally left alone so
// importing the engine never mutates global runtime state.
//
// Today it sets a soft memory limit (Go's GOMEMLIMIT). The Go runtime responds
// by running the GC more aggressively as the heap approaches the limit, which
// caps RSS growth and avoids the OS OOM-killer taking down the whole
// long-running MCP server (which would lose every loaded snapshot).
//
// An explicit GOMEMLIMIT environment variable always wins: the runtime already
// honors it, and an operator's choice must not be overridden. Only when it is
// unset do we auto-detect total system RAM and derive a limit from it.
//
// It is SILENT. It used to announce the limit on stderr, which meant every
// invocation of every command opened with a line about GC tuning — including
// `--version`, `--help`, and the read-only reports whose entire output is two
// lines of text. A process-wide default that is working correctly is not news,
// and printing it on each run trained the eye to skip the first line of enola's
// output, which is where its warnings also appear. The value is still reachable
// wherever it is genuinely diagnostic: MemoryLimit reports it, `enola doctor`
// prints it, and the MCP server logs it once at startup because that is the log
// somebody reads when investigating an OOM.
func ConfigureRuntime() {
	if _, ok := os.LookupEnv("GOMEMLIMIT"); ok {
		// The runtime has already applied the env value; defer to it.
		return
	}

	total := memory.TotalMemory()
	if total == 0 {
		// Detection failed (unsupported platform); leave the runtime default of
		// no limit rather than guess. Reported by MemoryLimit as "unset" rather
		// than warned about here — on a platform where detection never works, a
		// warning fires on every run forever and tells the user nothing they can
		// act on.
		return
	}

	debug.SetMemoryLimit(int64(float64(total) * memLimitFraction))
}

// MemoryLimit reports the soft memory limit in force and where it came from, as one
// line fit for a diagnostic report or a startup log.
//
// It reads the limit back from the runtime (SetMemoryLimit(-1) queries without
// changing) rather than remembering what ConfigureRuntime set, so it stays correct if
// something else adjusted it — and so it answers truthfully in a process that never
// called ConfigureRuntime at all, which is every library caller.
func MemoryLimit() (limit int64, source string) {
	limit = debug.SetMemoryLimit(-1)
	switch {
	case limit == noMemLimit:
		return limit, "unset (system memory could not be detected)"
	case os.Getenv("GOMEMLIMIT") != "":
		return limit, "GOMEMLIMIT=" + os.Getenv("GOMEMLIMIT")
	default:
		return limit, fmt.Sprintf("%.0f%% of %d MiB system RAM; override with GOMEMLIMIT",
			memLimitFraction*100, memory.TotalMemory()>>20)
	}
}

// MemoryLimitLine renders MemoryLimit as one human-readable line.
func MemoryLimitLine() string {
	limit, source := MemoryLimit()
	if limit == noMemLimit {
		return "no soft memory limit — " + source
	}
	return fmt.Sprintf("soft memory limit %d MiB (%s)", limit>>20, source)
}
