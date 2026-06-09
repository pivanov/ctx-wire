package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"ctx-wire/internal/commandpolicy"
	"ctx-wire/internal/shim"
)

// TestRunRecordsShimUseEvenWhenBypassed guards the auto-prune safety signal: a
// shim-routed command that ctx-wire BYPASSES (interactive, excluded, streaming)
// must still record shim use. Before the fix, Run returned at the bypass branch
// before recording, so a steering user whose commands were all bypassed read zero
// recorded use, and the auto-prune would wrongly treat their shims as unused.
func TestRunRecordsShimUseEvenWhenBypassed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shim usage recording is exercised on Unix")
	}
	reg := mustRegistry(t)

	realDir := t.TempDir()
	noop := filepath.Join(realDir, "noop")
	if err := os.WriteFile(noop, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", realDir)
	t.Setenv(shim.EnvLog, filepath.Join(t.TempDir(), "shims.jsonl"))
	t.Setenv(shim.EnvName, "noop") // a shim named 'noop' wired into us

	// Force the bypass path: an excluded command is bypassed before any filter.
	commandpolicy.SetExcludedCommands([]string{"noop"})
	t.Cleanup(func() { commandpolicy.SetExcludedCommands(nil) })

	code, err := Run(context.Background(), reg, "noop", nil)
	if err != nil || code != 0 {
		t.Fatalf("Run(noop) bypassed = (%d, %v), want (0, nil)", code, err)
	}

	count, _, err := shim.UsageSummary()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("a bypassed shim-routed command must still record use; got %d, want 1", count)
	}
}
