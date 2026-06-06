//go:build !windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

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
	// On cancel (Ctrl-C / timeout), give the process group a chance to clean up:
	// SIGTERM the whole group first, then the runtime escalates to SIGKILL after
	// WaitDelay if the process has not exited. An immediate SIGKILL would make
	// build/test tools skip cleanup and leave half-written artifacts.
	//
	// Tradeoff: the WaitDelay escalation kills the leader (os.Process.Kill), not
	// the whole group, so a same-group child that ignores SIGTERM can briefly
	// outlive us until its now-orphaned stdio pipes close. We accept that rare
	// edge in exchange for clean shutdown; a wall-clock group SIGKILL would risk
	// signalling a reused pgid.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
			return syscall.Kill(-pgid, syscall.SIGTERM)
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 3 * time.Second
	return cmd
}
