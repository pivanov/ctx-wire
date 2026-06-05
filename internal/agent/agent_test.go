package agent

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"claude", "claude"},
		{"Claude", "claude"},
		{"  CODEX  ", "codex"},
		{"agent-browser", "agent-browser"},
		{"copilot", "copilot"},
		{"", ""},
		{"   ", ""},
		{"claude code", ""},            // space is not allowed
		{"claude;rm -rf /", ""},        // shell metacharacters rejected
		{"CTX=evil", ""},               // '=' rejected
		{"a/b", ""},                    // slash rejected
		{"under_score", ""},            // underscore not in charset
		{string(make([]byte, 64)), ""}, // over-long (and NULs) rejected
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCurrentEnvWins(t *testing.T) {
	// An explicit CTX_WIRE_AGENT takes precedence over process-tree detection.
	t.Setenv(EnvName, "Codex")
	if got := Current(); got != "codex" {
		t.Errorf("Current() = %q, want %q", got, "codex")
	}
	// A malformed env value is rejected (not returned verbatim); Current then
	// falls back to detection, whose result is environment-dependent, so we only
	// assert the bad value never leaks through.
	t.Setenv(EnvName, "bad value")
	if got := Current(); got == "bad value" {
		t.Errorf("Current() returned the malformed env value verbatim: %q", got)
	}
}

func TestDetectFrom(t *testing.T) {
	// shell -> ctx-wire run wrapper -> the agent; the agent is found by walking up.
	procs := map[int]procInfo{
		100: {ppid: 90, cmd: "git status"},
		90:  {ppid: 80, cmd: "bash -c 'ctx-wire run git status'"},
		80:  {ppid: 1, cmd: "/usr/local/bin/codex serve"},
	}
	if got := detectFrom(100, procs); got != "codex" {
		t.Errorf("detectFrom = %q, want codex", got)
	}
	// Closest ancestor wins: codex run inside the Cursor editor is codex, not cursor.
	nested := map[int]procInfo{
		100: {ppid: 90, cmd: "bash"},
		90:  {ppid: 80, cmd: "node /opt/codex/cli.js"},
		80:  {ppid: 1, cmd: "/Applications/Cursor.app/Contents/MacOS/Cursor"},
	}
	if got := detectFrom(100, nested); got != "codex" {
		t.Errorf("detectFrom closest = %q, want codex (not cursor)", got)
	}
	// No agent anywhere in the chain.
	plain := map[int]procInfo{100: {ppid: 1, cmd: "/bin/zsh"}}
	if got := detectFrom(100, plain); got != "" {
		t.Errorf("detectFrom with no agent = %q, want %q", got, "")
	}
	// Unknown starting pid yields no attribution.
	if got := detectFrom(999, procs); got != "" {
		t.Errorf("detectFrom unknown pid = %q, want %q", got, "")
	}
}

func TestMatchAgent(t *testing.T) {
	cases := map[string]string{
		"/Users/x/.local/bin/claude":        "claude",
		"node /opt/codex/index.js":          "codex",
		"bash -c 'ctx-wire run git status'": "", // ctx-wire / shells must not match
		"/bin/zsh":                          "",
	}
	for cmd, want := range cases {
		if got := matchAgent(cmd); got != want {
			t.Errorf("matchAgent(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestKnownAreWellFormed(t *testing.T) {
	for _, name := range Known {
		if Normalize(name) != name {
			t.Errorf("Known agent %q does not survive Normalize", name)
		}
	}
}
