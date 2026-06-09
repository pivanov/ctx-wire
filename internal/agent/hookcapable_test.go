package agent

import "testing"

// The hook-capable set decides whether the shim auto-wires under an agent. It
// must be exactly the agents with an automatic rewrite path (hook or plugin);
// misclassifying a steering-only agent here would strip its only coverage, and
// misclassifying a hook agent the other way reintroduces the $() corruption.
func TestIsHookCapable(t *testing.T) {
	hookCapable := []string{"claude", "codex", "cursor", "gemini", "copilot", "opencode", "pi", "hermes"}
	for _, a := range hookCapable {
		if !IsHookCapable(a) {
			t.Errorf("%q has an auto-rewrite hook/plugin and must be hook-capable", a)
		}
	}
	steeringOrMCP := []string{"cline", "windsurf", "kilocode", "antigravity", "vscode", "visualstudio", ""}
	for _, a := range steeringOrMCP {
		if IsHookCapable(a) {
			t.Errorf("%q has no auto-rewrite (the shim is its only coverage); it must NOT be hook-capable", a)
		}
	}
	if !IsHookCapable("CLAUDE") {
		t.Error("IsHookCapable must normalize case")
	}
}
