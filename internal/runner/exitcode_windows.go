//go:build windows

package runner

import "os/exec"

// signalExitCode has no signal concept on Windows; callers fall back to
// ExitError.ExitCode().
func signalExitCode(ee *exec.ExitError) (int, bool) {
	return 0, false
}
