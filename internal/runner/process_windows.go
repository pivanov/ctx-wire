//go:build windows

package runner

import (
	"context"
	"os"
	"os/exec"

	"ctx-wire/internal/shim"
)

func newCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	// Stop nested shims from re-wiring inside a wrapped command (see process_unix.go).
	cmd.Env = append(os.Environ(), shim.EnvDisable+"=1")
	return cmd
}
