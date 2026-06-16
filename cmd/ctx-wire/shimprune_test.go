package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestShouldFlagRedundantShims is the truth table for the redundant-shims advisory
// (no deletion happens; this only decides whether to NUDGE). Load-bearing gates:
// uses == 0 (any recorded shim use means a steering agent relied on them -> don't
// nudge) and active > 0 (only flag shims actually on the hot path, not harmless
// shadowed ones). Counts are aggregated across every managed shim dir; the keep
// marker is handled by the caller, not this predicate.
func TestShouldFlagRedundantShims(t *testing.T) {
	cases := []struct {
		name        string
		installed   int
		active      int
		uses        int
		hookCovered bool
		steering    bool
		want        bool
	}{
		{"hook-only, unused, on hot path -> nudge (the common case)", 2, 2, 0, true, false, true},
		{"recorded use (steering relied on them) -> quiet", 2, 2, 7, true, false, false},
		{"installed but shadowed (active 0) -> quiet (no latency to fix)", 2, 0, 0, true, false, false},
		{"no hook/plugin coverage -> quiet (shims may be only coverage)", 2, 2, 0, false, false, false},
		{"steering agent configured here -> quiet", 2, 2, 0, true, true, false},
		{"nothing installed -> quiet", 0, 0, 0, true, false, false},
	}
	for _, tc := range cases {
		got := shouldFlagRedundantShims(tc.installed, tc.active, tc.uses, tc.hookCovered, tc.steering)
		if got != tc.want {
			t.Errorf("%s: shouldFlagRedundantShims = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestHookOrPluginCoverageConfiguredSiblingDir verifies that a ctx-wire Claude
// hook installed only in a sibling config dir (e.g. ~/.claude-main) is
// detected, not just hooks in the primary ~/.claude directory.
func TestHookOrPluginCoverageConfiguredSiblingDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	// Primary ~/.claude: exists with settings.json (no needle) and projects/.
	primaryDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(primaryDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primaryDir, "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sibling ~/.claude-main: has settings.json WITH the needle and projects/.
	siblingDir := filepath.Join(home, ".claude-main")
	if err := os.MkdirAll(filepath.Join(siblingDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	siblingSettings := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"ctx-wire hook claude"}]}]}}`
	if err := os.WriteFile(filepath.Join(siblingDir, "settings.json"), []byte(siblingSettings), 0o644); err != nil {
		t.Fatal(err)
	}

	if !hookOrPluginCoverageConfigured(home) {
		t.Error("hookOrPluginCoverageConfigured = false, want true (hook lives in sibling ~/.claude-main)")
	}
}
