//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// signalExitCode maps a signal-terminated child to the conventional 128+signal
// shell exit code (137 for SIGKILL, 130 for SIGINT). exec.ExitError.ExitCode()
// returns -1 for a signal death, which a shell surfaces as 255.
func signalExitCode(ee *exec.ExitError) (int, bool) {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), true
	}
	return 0, false
}
