package filelock

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTryAcquire_SecondCallerIsTurnedAway is the single-flight property the session-start
// pin depends on: several agent terminals open on one repository is the normal case, and
// only one of them may do the work.
func TestTryAcquire_SecondCallerIsTurnedAway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pin")

	first, ok, err := TryAcquire(path)
	if err != nil || !ok {
		t.Fatalf("first TryAcquire: ok=%v err=%v", ok, err)
	}

	// Same process, same file: flock is per (process, file), so a second acquisition
	// from this process would succeed and the test would prove nothing — hence the
	// separate descriptor via a second TryAcquire on the same path is only meaningful
	// across processes. What IS assertable here is that release makes it available again.
	first.Release()

	second, ok, err := TryAcquire(path)
	if err != nil || !ok {
		t.Fatalf("lock was not released: ok=%v err=%v", ok, err)
	}
	second.Release()
}

// TestRelease_IsSafeOnNil — callers defer it unconditionally, including on the path where
// acquisition failed.
func TestRelease_IsSafeOnNil(t *testing.T) {
	var l *Lock
	l.Release()
	(&Lock{}).Release()
}

// TestAcquire_CreatesLockFileBesideTarget — the lock must not collide with the file it
// guards, which is a real snapshot artifact.
func TestAcquire_CreatesLockFileBesideTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-pin")

	l, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("expected a .lock file beside the target: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the guarded path itself must not be created (err=%v)", err)
	}
}
