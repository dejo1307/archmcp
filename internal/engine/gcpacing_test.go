package engine

import (
	"os"
	"runtime/debug"
	"testing"
)

// The pacing change must be invisible outside the snapshot. A generate that lowered
// GOGC and left it lowered would quietly re-pace the collector for the rest of a
// long-running server's life, which is a far bigger decision than the one being made
// here — and it would be invisible, because nothing else reports GOGC.
func TestSnapshotGCPercent_RestoresPrevious(t *testing.T) {
	withoutGOGC(t)

	// SetGCPercent returns the value it replaced, which makes it both the setter and
	// the only way to read the current value.
	original := debug.SetGCPercent(140)
	t.Cleanup(func() { debug.SetGCPercent(original) })

	restore := snapshotGCPercent()
	during := debug.SetGCPercent(snapshotGCPercentValue)
	restore()
	after := debug.SetGCPercent(140)

	if during != snapshotGCPercentValue {
		t.Errorf("during a snapshot GOGC = %d, want %d", during, snapshotGCPercentValue)
	}
	if after != 140 {
		t.Errorf("after restore GOGC = %d, want the original 140", after)
	}
}

// An operator who set GOGC has said something, and a default that overrides it is
// not a default — the same rule ConfigureRuntime applies to GOMEMLIMIT.
func TestSnapshotGCPercent_ExplicitEnvWins(t *testing.T) {
	t.Setenv("GOGC", "200")

	original := debug.SetGCPercent(200)
	t.Cleanup(func() { debug.SetGCPercent(original) })

	restore := snapshotGCPercent()
	during := debug.SetGCPercent(200)
	restore()

	if during != 200 {
		t.Errorf("GOGC = %d during a snapshot, want the environment's 200 left alone", during)
	}
}

// withoutGOGC removes GOGC for the duration of the test and puts back whatever was
// there. t.Setenv cannot express "unset" — a variable set to "" is still set as far
// as os.LookupEnv is concerned, which is the check snapshotGCPercent makes.
func withoutGOGC(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv("GOGC")
	if err := os.Unsetenv("GOGC"); err != nil {
		t.Fatalf("unsetting GOGC: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("GOGC", prev)
			return
		}
		_ = os.Unsetenv("GOGC")
	})
}
