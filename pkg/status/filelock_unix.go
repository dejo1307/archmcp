//go:build !windows

package status

import (
	"os"
	"syscall"
)

// lockFile is an exclusive advisory lock held across processes for the duration
// of a read-modify-write of a shared counter file.
type lockFile struct {
	f *os.File
}

// acquireLock blocks until it holds an exclusive lock on path+".lock". The lock
// file itself is never read — only its descriptor matters — so a leftover file
// from a crashed process is harmless: flock is released by the kernel when the
// owning process dies.
//
// A failure to lock is returned rather than fatal; callers degrade to an
// unsynchronized write, which is no worse than the pre-lock behaviour.
func acquireLock(path string) (*lockFile, error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &lockFile{f: f}, nil
}

// release unlocks and closes the lock file. Safe on a nil receiver so callers
// can defer it unconditionally.
func (l *lockFile) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}
