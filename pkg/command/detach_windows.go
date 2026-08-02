//go:build windows

package command

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detachable reports whether this platform can start a process that outlives its parent.
const detachable = true

// detach configures cmd to run detached from the parent's console and in its own process
// group, the Windows counterpart to setsid.
//
// DETACHED_PROCESS gives the child no console to inherit, so it is not signalled when the
// parent's console closes; CREATE_NEW_PROCESS_GROUP keeps a group-wide kill of the hook
// from reaching it. Without both, a snapshot that outlives a millisecond-long hook would
// be terminated the moment the hook returned.
// The struct is syscall.SysProcAttr — the type os/exec expects — while the flag
// constants come from x/sys/windows, which defines DETACHED_PROCESS where syscall does
// not.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}
