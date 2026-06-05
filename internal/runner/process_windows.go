//go:build windows

package runner

import (
	"context"
	"os/exec"
)

func newCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
