//go:build !windows

package status

import (
	"os"
	"syscall"
)

// isProcessAlive reports whether a process with the given PID is running.
// On Unix, signal 0 performs the existence/permission check without delivering
// a signal.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return syscall.Kill(p.Pid, 0) == nil
}
