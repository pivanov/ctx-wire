//go:build !windows

package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/agent"
)

// The shell shim's agent split must stay in sync with agent.HookCapable: a
// hook/plugin-capable agent must NEVER be wired (it is passed through, its own
// rewrite covers model-visible commands), or the $() capture corruption returns.
// Steering-only / opt-in agents must still be wired (the shim is their only
// coverage).
func TestShellShimAgentSplitMatchesHookCapable(t *testing.T) {
	s := shimScript("git", "/opt/ctx-wire/ctx-wire")
	for _, a := range agent.HookCapable {
		if strings.Contains(s, "detected_agent="+a) {
			t.Errorf("hook-capable agent %q is still wired (detected_agent=%s); it must pass through", a, a)
		}
	}
	for _, a := range []string{"cline", "windsurf", "kilocode", "antigravity", "vscode", "visualstudio"} {
		if !strings.Contains(s, "detected_agent="+a) {
			t.Errorf("steering/opt-in agent %q must still be wired by the shim", a)
		}
	}
}

// End-to-end: with CTX_WIRE_AGENT set to a hook-capable agent (as inherited by a
// subprocess of a hook-wrapped command), the shim must pass through and exec the
// real binary, NOT wrap it through ctx-wire. This is what stops result=$(cat ...)
// being silently filtered when the agent already rewrote the model-visible call.
func TestShimPassesThroughUnderHookCapableAgentEnv(t *testing.T) {
	shimDir := t.TempDir()
	realBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(realBin, "git"), []byte("#!/bin/sh\necho FAKEGIT \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shimPath := filepath.Join(shimDir, "git")
	if err := os.WriteFile(shimPath, []byte(shimScript("git", "/nonexistent/ctx-wire")), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(shimPath, "status")
	cmd.Env = []string{
		"PATH=" + shimDir + string(os.PathListSeparator) + realBin,
		"CTX_WIRE_AGENT=claude", // hook-capable: the shim must pass through
	}
	out, err := cmd.CombinedOutput()
	if got := string(out); !strings.Contains(got, "FAKEGIT status") {
		t.Errorf("under a hook-capable CTX_WIRE_AGENT the shim must passthrough-exec the real git (err=%v):\n%s", err, got)
	}
}
