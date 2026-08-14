package engine

import (
	"os"
	"runtime/debug"
)

// snapshotGCPercentValue is the GOGC in force while a snapshot is being generated.
//
// Generation's peak heap is roughly two thirds garbage the collector has not swept
// yet — a snapshot allocates tens of gigabytes to retain a few hundred megabytes,
// and at the default GOGC=100 the heap is allowed to double over the live set before
// anything reclaims it. Collecting more often trades CPU for that headroom, and the
// trade is unusually good here because extraction is bound on tree-sitter parsing
// through cgo rather than on the collector.
//
// Measured on dotnet/runtime, cold, otherwise identical runs (extraction only; see
// WriteArtifacts for the separate measurement of the artifact-writing window):
//
//	GOGC   peak heap   wall
//	100    1,481 MiB   56.3 s
//	50     1,131 MiB   56.0 s   (-23.6% peak, no cost)
//	25     1,003 MiB   58.5 s   (-32.3% peak, +3.9%)
//
// 25 rather than 50 because the peak is what decides whether the process survives on
// a smaller machine, and 4% of a snapshot is a fair price for a third of it. Going
// lower was not measured and should not be assumed to keep paying: below some point
// the collector starts running continuously and the curve turns.
const snapshotGCPercentValue = 25

// snapshotGCPercent lowers GOGC for the duration of one piece of snapshot work and
// returns the function that restores it. Call as `defer snapshotGCPercent()()`.
//
// Both halves of a snapshot use it — GenerateSnapshot and WriteArtifacts — because
// both allocate heavily against a live store, and covering only the first left the
// actual peak unpaced. See the comment at WriteArtifacts. The brief return to the
// default between the two calls is immaterial: nothing of size allocates there.
//
// It is scoped to the call rather than set once at startup, deliberately. GOGC is
// process-global, and enola is imported as a library as well as run as a binary;
// pkg/bootstrap.ConfigureRuntime holds the line that importing the engine must not
// mutate global runtime state. A setting that is in force only while this function
// runs, and is put back on every path including a failure, is the narrowest form of
// the change that still covers the window where the memory is actually spent.
//
// An explicit GOGC in the environment always wins, mirroring how ConfigureRuntime
// defers to an explicit GOMEMLIMIT: an operator who has tuned the collector has
// said something, and a default that overrides it is not a default.
func snapshotGCPercent() func() {
	if _, ok := os.LookupEnv("GOGC"); ok {
		return func() {}
	}
	prev := debug.SetGCPercent(snapshotGCPercentValue)
	return func() { debug.SetGCPercent(prev) }
}
