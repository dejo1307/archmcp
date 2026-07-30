//go:build !windows

// Package filelock provides an exclusive advisory lock held across processes.
//
// Two callers need it for different reasons, which is why it has both a blocking and
// a non-blocking form:
//
//   - the usage counters serialize a read-modify-write and must WAIT, because losing an
//     update loses data;
//   - the session-start baseline pin is single-flight and must NOT wait, because several
//     agent terminals open on one repository is the normal case, and queueing them would
//     turn one wasted snapshot into N of them running back to back.
//
// The lock is advisory and owned by the kernel: it is released when the holding process
// dies, so a leftover lock file from a crash cannot wedge later runs. The file's contents
// are never read — only the descriptor matters.
package filelock

import (
	"os"
	"syscall"
)

// Lock is a held file lock. Release is safe on a nil receiver so callers may defer it
// unconditionally.
type Lock struct {
	f *os.File
}

// Acquire blocks until it holds an exclusive lock on path+".lock".
//
// A failure is returned rather than fatal; callers degrade to an unsynchronized
// operation, which is no worse than not locking at all.
func Acquire(path string) (*Lock, error) {
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// TryAcquire takes the lock if it is free and reports ok=false immediately if another
// process holds it, rather than waiting.
//
// "Someone else is already doing this" is a normal outcome for a single-flight task, not
// an error, so it is reported separately from a genuine failure to lock.
func TryAcquire(path string) (l *Lock, ok bool, err error) {
	f, err := open(path)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil // held elsewhere — expected
		}
		return nil, false, err
	}
	return &Lock{f: f}, true, nil
}

// Release unlocks and closes the lock file.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}

func open(path string) (*os.File, error) {
	return os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
}
