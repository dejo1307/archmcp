//go:build !windows

package command

import (
	"os/exec"
	"syscall"
)

// detachable reports whether this platform can start a process that outlives its parent
// and survives the parent's process group being killed.
const detachable = true

// detach configures cmd to run in a new session, so it is neither killed when the hook
// process exits nor when the agent's harness kills the hook's process group on timeout.
//
// Setsid rather than Setpgid: a hook is run by the agent, which may well terminate the
// whole group when the hook returns or times out. A new session detaches from that group
// AND from the controlling terminal, which is what keeps a 10-second snapshot alive after
// a hook that returned in milliseconds.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
