package command

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/hookstate"
)

// The heartbeat is the one thing `install --hooks` writes outside the agents' own config
// files, and in a repository that has never been snapshotted it is also what creates the
// output directory. Uninstall has to take both back, or the reversal stops one empty
// directory short of complete.
func TestClearHookHeartbeat_RemovesTheDirectoryItCreated(t *testing.T) {
	repo := t.TempDir()
	outDir := filepath.Join(repo, ".enola")

	hookstate.RecordInstalled(outDir, "enola")
	if _, err := os.Stat(hookstate.Path(outDir)); err != nil {
		t.Fatalf("the heartbeat was not written: %v", err)
	}

	clearHookHeartbeat(repo, outDir)

	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(outDir)
		t.Errorf("uninstall left the output directory behind (err=%v, %d entries)", err, len(entries))
	}
}

// It is the emptiness that licenses the removal, never the uninstall. A baseline or a
// snapshot in there is the user's work, and losing it to an uninstall would cost far more
// than the empty directory this is here to clean up.
func TestClearHookHeartbeat_KeepsAnOutputDirectoryHoldingWork(t *testing.T) {
	repo := t.TempDir()
	outDir := filepath.Join(repo, ".enola")

	hookstate.RecordInstalled(outDir, "enola")
	baseline := filepath.Join(outDir, "baseline.json")
	if err := os.WriteFile(baseline, []byte(`{"pinned":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	clearHookHeartbeat(repo, outDir)

	if _, err := os.Stat(baseline); err != nil {
		t.Errorf("uninstall removed a pinned baseline: %v", err)
	}
	if _, err := os.Stat(hookstate.Path(outDir)); !os.IsNotExist(err) {
		t.Errorf("the heartbeat survived an uninstall (err=%v)", err)
	}
}

// An output directory configured as the repository itself is not ours to remove, however
// empty it happens to be.
func TestClearHookHeartbeat_NeverRemovesTheRepositoryItself(t *testing.T) {
	repo := t.TempDir()

	hookstate.RecordInstalled(repo, "enola")
	clearHookHeartbeat(repo, repo)

	if _, err := os.Stat(repo); err != nil {
		t.Errorf("uninstall removed the repository directory: %v", err)
	}
}
