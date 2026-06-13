package main

import (
	"os"
	"testing"
)

// TestMain unsets the agent config-directory env vars before any test runs, so a
// command-layer test (cmdInit/cmdUninstall exercise real install/uninstall) that
// sets HOME to a temp dir but inherits the developer's real CLAUDE_CONFIG_DIR
// cannot mutate live config. Mirrors the guard in internal/install. Tests that
// need a specific dir set it explicitly with t.Setenv.
func TestMain(m *testing.M) {
	for _, k := range []string{
		"CLAUDE_CONFIG_DIR",
		"CODEX_HOME",
		"GEMINI_HOME",
		"PI_CODING_AGENT_DIR",
		"HERMES_HOME",
	} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
