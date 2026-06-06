//go:build !windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"ctx-wire/internal/shim"
)

func newCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	// Stop nested shims from re-wiring inside a wrapped command. The model-bound
	// output is already captured at this level, so inner pipeline stages and
	// helper subprocesses must run raw (byte-exact), matching the rewrite layer's
	// "only the final pipeline stage is wrapped" contract.
	cmd.Env = append(os.Environ(), shim.EnvDisable+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			return cmd.Process.Kill()
		}
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return cmd
}
