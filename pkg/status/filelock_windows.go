//go:build windows

package status

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile is an exclusive lock held across processes for the duration of a
// read-modify-write of a shared counter file.
type lockFile struct {
	f  *os.File
	ov windows.Overlapped
}

// acquireLock blocks until it holds an exclusive lock on path+".lock", using
// LockFileEx — the Windows counterpart to flock. Like flock, the lock is
// released by the OS if the owning process dies, so a leftover lock file from a
// crash does not wedge later runs.
func acquireLock(path string) (*lockFile, error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	l := &lockFile{f: f}
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

// release unlocks and closes the lock file. Safe on a nil receiver so callers
// can defer it unconditionally.
func (l *lockFile) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &l.ov)
	_ = l.f.Close()
}
