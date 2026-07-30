//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Lock is a held file lock. Release is safe on a nil receiver so callers may defer it
// unconditionally.
type Lock struct {
	f  *os.File
	ov windows.Overlapped
}

// Acquire blocks until it holds an exclusive lock on path+".lock", using LockFileEx —
// the Windows counterpart to flock. Like flock, the lock is released by the OS if the
// owning process dies, so a leftover lock file from a crash does not wedge later runs.
func Acquire(path string) (*Lock, error) {
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	l := &Lock{f: f}
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0, &l.ov,
	); err != nil {
		_ = f.Close()
		return nil, err
	}
	return l, nil
}

// TryAcquire takes the lock if it is free and reports ok=false immediately if another
// process holds it. LOCKFILE_FAIL_IMMEDIATELY turns contention into ERROR_LOCK_VIOLATION
// rather than a wait, which is the documented Windows equivalent of flock's EWOULDBLOCK.
func TryAcquire(path string) (l *Lock, ok bool, err error) {
	f, err := open(path)
	if err != nil {
		return nil, false, err
	}
	lk := &Lock{f: f}
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &lk.ov,
	); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, false, nil // held elsewhere — expected
		}
		return nil, false, err
	}
	return lk, true, nil
}

// Release unlocks and closes the lock file.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &l.ov)
	_ = l.f.Close()
}

func open(path string) (*os.File, error) {
	return os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
}
