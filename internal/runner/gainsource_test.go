package runner

import (
	"testing"

	"ctx-wire/internal/shim"
)

// gainSource must use a reliable signal: the shim marker for shim, the explicit
// --agent hook marker for hook, and run for everything else. An attributed agent
// alone (process-tree detection on a manual run inside an agent session) must NOT
// read as hook, or the hook-vs-shim benchmark is meaningless.
func TestGainSource(t *testing.T) {
	cases := []struct {
		name      string
		shimEnv   string
		sourceEnv string
		agentEnv  string
		want      string
	}{
		{"shim wins", "git", "hook", "claude", "shim"},
		{"explicit hook marker", "", "hook", "claude", "hook"},
		{"explicit mcp marker (the MCP server sets this)", "", "mcp", "claude", "mcp"},
		{"manual run inside agent session is not hook", "", "", "claude", "run"},
		{"plain run", "", "", "", "run"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(shim.EnvName, c.shimEnv)
			t.Setenv(EnvSource, c.sourceEnv)
			t.Setenv("CTX_WIRE_AGENT", c.agentEnv)
			if got := gainSource(); got != c.want {
				t.Fatalf("gainSource() = %q, want %q", got, c.want)
			}
		})
	}
}
