package command

import (
	"os/exec"
	"strings"
	"testing"
)

// captureSpawn swaps the process starter for one that records the argument lists it was
// handed and starts nothing.
func captureSpawn(t *testing.T) *[][]string {
	t.Helper()
	var got [][]string
	prev := spawn
	spawn = func(cmd *exec.Cmd) error {
		got = append(got, cmd.Args[1:]) // args[0] is the executable path
		return nil
	}
	t.Cleanup(func() { spawn = prev })
	return &got
}

// The child must be started with the arguments the hook dispatcher actually routes.
// Nothing else connects these two — a renamed event would leave the spawner starting a
// process that this binary silently treats as an unknown hook and exits 0 from, which is
// indistinguishable from the check simply never finding an update.
func TestSpawnUpdateRefreshStartsTheRefreshChild(t *testing.T) {
	got := captureSpawn(t)
	seedUpdateCache(t, "", "") // isolated HOME, no cache: a refresh is due

	SpawnUpdateRefresh()

	if len(*got) != 1 {
		t.Fatalf("started %d child processes with no cache present, want 1", len(*got))
	}
	if want := "hook " + updateRefreshEvent; strings.Join((*got)[0], " ") != want {
		t.Errorf("child args = %q, want %q", (*got)[0], want)
	}
}

// The gate is the whole reason this is affordable on the critical path. Without it every
// enola command would fork a process to re-answer a question already answered on disk.
func TestSpawnUpdateRefreshIsGated(t *testing.T) {
	t.Run("cache is fresh", func(t *testing.T) {
		got := captureSpawn(t)
		seedUpdateCache(t, "0.3.12", "v999") // written just now, so inside the TTL

		SpawnUpdateRefresh()

		if len(*got) != 0 {
			t.Errorf("started %d child processes with a fresh cache, want 0", len(*got))
		}
	})

	t.Run("suppressed", func(t *testing.T) {
		got := captureSpawn(t)
		seedUpdateCache(t, "", "")
		t.Setenv("CI", "true")

		SpawnUpdateRefresh()

		if len(*got) != 0 {
			t.Errorf("started %d child processes under CI, where the result could not be reported anyway", len(*got))
		}
	})
}
