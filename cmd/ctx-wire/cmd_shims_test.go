package main

import (
	"testing"

	"ctx-wire/internal/agent"
)

// TestInitShimGatingPartition locks which agents `init` skips PATH shims for.
// Hook/plugin-capable agents are covered by their hook/plugin (and shims early on
// PATH slow shell startup), so init no longer installs default shims for them;
// steering-only agents still get shims because the shim is their only coverage.
// Pinning the partition forces any newly added agent onto a deliberate side.
func TestInitShimGatingPartition(t *testing.T) {
	gated := []string{"claude", "codex", "cursor", "gemini", "copilot", "opencode", "pi", "hermes"}
	shimmed := []string{"cline", "windsurf", "kilocode", "antigravity", "vscode", "visualstudio"}

	for _, a := range gated {
		if !agent.IsHookCapable(a) {
			t.Errorf("%s should be hook-capable, so `init` skips its PATH shims", a)
		}
	}
	for _, a := range shimmed {
		if agent.IsHookCapable(a) {
			t.Errorf("%s is steering-only and must still get PATH shims from `init`", a)
		}
	}
}
