//go:build windows

package status

import "golang.org/x/sys/windows"

// stillActive is the exit code Windows reports for a process that is still
// running (STILL_ACTIVE). A process that genuinely exits with code 259 would be
// misreported as alive, which is an acceptable edge case for a status check.
const stillActive = 259

// isProcessAlive reports whether a process with the given PID is running.
// Windows has no signal-0 check, so we open the process and query its exit code.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
