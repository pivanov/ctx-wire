//go:build windows

package gain

import "syscall"

// stillActive is GetExitCodeProcess's STILL_ACTIVE return: the process has not
// exited. Any other value means it is gone.
const stillActive = 259

// processAlive reports whether a process with the given PID is still running.
// os.FindProcess alone is not enough on Windows: OpenProcess can succeed for a
// process that already exited but whose kernel object lingers while another
// handle stays open, so a bare "did the open succeed" check reports dead
// holders as alive. GetExitCodeProcess distinguishes them: STILL_ACTIVE means
// running, anything else means exited. (The 30s TTL backstop in staleGainLock
// still covers the case where the handle cannot be opened at all.)
func processAlive(pid int) bool {
	const queryLimitedInformation = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	h, err := syscall.OpenProcess(queryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
