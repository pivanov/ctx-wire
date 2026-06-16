//go:build !windows

package recent

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether a process with the given PID currently exists.
// Signal 0 performs no delivery but still runs the kernel's permission and
// existence checks: nil means alive, EPERM means alive but owned by another
// user, and ESRCH means gone.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, os.ErrPermission)
}
