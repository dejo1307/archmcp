// Package diff re-exports enola's internal snapshot-diff engine as public type
// aliases and functions so out-of-module code (e.g. enola-enterprise) can compute
// and consume architecture deltas — for a future governance/metric-delta layer —
// without importing the internal package directly.
//
// These are Go ALIASES, identical to the internal types.
package diff

import internal "github.com/enola-labs/enola/internal/diff"

// Delta types (aliases — identical to the internal types).
type (
	SnapshotDiff = internal.SnapshotDiff
	Edge         = internal.Edge
	FactChange   = internal.FactChange
)

// Compute returns the delta from baseline to current. See internal/diff for the
// ratchet semantics (delta-only; pre-existing state never reported).
var Compute = internal.Compute

// KindCounts tallies facts by kind (used for structural-change summaries).
var KindCounts = internal.KindCounts
