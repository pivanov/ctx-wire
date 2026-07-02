package install

import (
	"os"
	"testing"
)

// configDirEnvVars are the agent config-directory environment variables the path
// resolvers in this package honor (ClaudeConfigDirs adds CLAUDE_CONFIG_DIR
// first, CodexHooksPath honors CODEX_HOME, and so on). A developer's real shell
// commonly sets some of these, CLAUDE_CONFIG_DIR especially, for a custom Claude
// config dir.
var configDirEnvVars = []string{
	"CLAUDE_CONFIG_DIR",
	"CODEX_HOME",
	"GEMINI_HOME",
	"COPILOT_HOME",
	"PI_CODING_AGENT_DIR",
	"HERMES_HOME",
}

// TestMain unsets those env vars before any test runs, so path resolution falls
// back to each test's temp HOME and no test can mutate the developer's live
// agent config. This guards a real footgun: a test that set HOME to a temp dir
// but forgot to override CLAUDE_CONFIG_DIR would otherwise resolve the leaked
// real dir, and an install/uninstall there strips the ctx-wire hook from the
// developer's actual ~/.claude config. Tests that need a specific config dir
// still set it explicitly with t.Setenv (which restores to the unset state
// afterward), so this only removes the unsafe inherited default.
func TestMain(m *testing.M) {
	for _, k := range configDirEnvVars {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}

// TestConfigDirEnvNeutralized proves the TestMain guard holds: with no per-test
// override, every config-dir env var must read empty, so path resolution uses
// the test's temp HOME and never the developer's live config. It fails if
// TestMain is removed or a resolver gains an env var the guard does not clear.
func TestConfigDirEnvNeutralized(t *testing.T) {
	for _, k := range configDirEnvVars {
		if v := os.Getenv(k); v != "" {
			t.Errorf("%s = %q at test time; TestMain must unset it so tests cannot mutate real config", k, v)
		}
	}
}
