package command

import (
	"os"
	"os/exec"

	"github.com/enola-labs/enola/internal/updatecheck"
)

// updateRefreshEvent is the hook event that performs the refresh. It lives in the `hook`
// namespace because that is where enola's unattended, silent, always-exit-0 work already
// lives — the rules this needs are the rules that namespace already enforces.
//
// It is not documented as a command anyone types. `enola hook <unknown>` is a no-op by
// design, so a future build that drops this event is not a build that breaks on it.
const updateRefreshEvent = "update-check"

// SpawnUpdateRefresh starts a detached child that refreshes the update cache, and returns
// immediately. It is the WRITE side of the update check; every other surface only reads
// the file this child writes.
//
// It exists because the two original writers do not cover the person most in need of the
// notice. Refresh ran in exactly two places — the session-start hook's detached child and
// the MCP server's boot goroutine — so someone who installs the binary and runs `enola
// check` from a shell, with no agent hooks installed and no MCP server, never wrote a
// cache and therefore was never told anything. Every reading surface was live and every
// one of them read an empty file, indefinitely. That is the failure this closes: the
// notice was not wrong, it was unreachable.
//
// The spawn, not a call: the package's founding rule is that no command a person waits on
// may contain the network. A detached child costs one process spawn, is independent of
// how slow the network is, and cannot delay or fail the command that started it. Due()
// gates the spawn so the cost is paid at most once per ttl rather than on every command.
//
// Silent throughout, like everything else here — a caller invokes it for effect and has
// nothing to do about a failure to check for updates.
func SpawnUpdateRefresh() {
	if !detachable || !updatecheck.Due() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "hook", updateRefreshEvent)
	// No stdio. The child outlives this command, and an inherited stderr would let it
	// write into a terminal that has already moved on to the next prompt.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	// Start, never Wait: waiting is precisely what detaching exists to avoid.
	_ = spawn(cmd)
}

// spawn is exec.Cmd.Start, indirected so a test can see the child that WOULD be started
// without starting one — os.Executable() under `go test` is the test binary, and a test
// that really forked it would re-enter the suite as `hook update-check`.
var spawn = func(cmd *exec.Cmd) error { return cmd.Start() }
