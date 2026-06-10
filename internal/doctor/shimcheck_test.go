package doctor

import (
	"strings"
	"testing"

	"ctx-wire/internal/ui"
)

// TestShimPathChecks pins the resolution-based advisory: the decision keys off the
// active count (commands that actually resolve to a shim, aggregated across dirs),
// not PATH directory order.
func TestShimPathChecks(t *testing.T) {
	const dir, total = "/home/u/.local/bin", 3

	cases := []struct {
		name        string
		installed   int
		active      int
		hookCovered bool
		wantName    string
		wantStatus  Status
	}{
		{"active + hook-covered -> startup-cost warning (the slow-terminal case)", 3, 2, true, "startup cost", Warn},
		{"active, steering-only -> shims first on PATH is fine", 3, 2, false, "PATH", OK},
		{"installed but shadowed + hook-covered -> optional, removable", 3, 0, true, "shims", Off},
		{"installed but shadowed, steering-only -> promote on PATH", 3, 0, false, "PATH", Warn},
	}
	for _, tc := range cases {
		got := shimPathChecks(dir, tc.installed, tc.active, total, tc.hookCovered)
		if len(got) != 1 {
			t.Fatalf("%s: got %d checks, want 1", tc.name, len(got))
		}
		if got[0].Name != tc.wantName || got[0].Status != tc.wantStatus {
			t.Errorf("%s: got {%q, %s}, want {%q, %s}", tc.name, got[0].Name, got[0].Status, tc.wantName, tc.wantStatus)
		}
	}

	// The load-bearing guarantee: the startup-cost warning fires ONLY when shims
	// are actually on the hot path AND a hook/plugin covers them. A hook-covered
	// user whose shims are shadowed must NOT be told they have a startup cost (that
	// false alarm is exactly the methodology error the reviews caught).
	if got := shimPathChecks(dir, 3, 0, total, true); got[0].Name == "startup cost" {
		t.Error("must not warn about startup cost when no managed command resolves to a shim")
	}
	// The warning names the cheap fix.
	if got := shimPathChecks(dir, 3, 2, total, true); !strings.Contains(got[0].Detail, "ctx-wire shims uninstall") {
		t.Errorf("startup-cost warning should point at `ctx-wire shims uninstall`, got %q", got[0].Detail)
	}
	// No shims installed at all -> no advisory line.
	if got := shimPathChecks(dir, 0, 0, total, true); got != nil {
		t.Errorf("no installed shims -> no advisory, got %+v", got)
	}
}

// TestShimInstalledCheck pins the "installed" check, whose 0-shims branch used to
// Warn unconditionally and tell every user to run `ctx-wire init <agent>`. For a
// hook/plugin-covered agent, zero shims is the correct, intended state (the agent
// is wired through its hook), so that branch must report Off, not Warn.
func TestShimInstalledCheck(t *testing.T) {
	const dir, total = "/home/u/.local/bin", 3

	cases := []struct {
		name        string
		installed   int
		skipped     int
		hookCovered bool
		wantStatus  Status
	}{
		{"all installed", 3, 0, false, OK},
		{"all installed, hook-covered too", 3, 0, true, OK},
		{"partial install", 2, 0, false, OK},
		{"zero shims, hook-covered -> not a problem (the false-positive we fixed)", 0, 0, true, Off},
		{"zero shims, nothing wired -> actionable", 0, 0, false, Warn},
	}
	for _, tc := range cases {
		got := shimInstalledCheck(tc.installed, total, tc.skipped, dir, tc.hookCovered)
		if got.Name != "installed" || got.Status != tc.wantStatus {
			t.Errorf("%s: got {%q, %s}, want {installed, %s} (%q)", tc.name, got.Name, got.Status, tc.wantStatus, got.Detail)
		}
	}

	// The load-bearing guarantee: a hook-covered user with zero shims must never be
	// told to run init (it would install nothing for them) and must not be a Warn.
	got := shimInstalledCheck(0, total, 0, dir, true)
	if got.Status == Warn {
		t.Error("zero shims + hook-covered must not Warn")
	}
	if strings.Contains(got.Detail, "ctx-wire init") {
		t.Errorf("hook-covered user must not be told to run init, got %q", got.Detail)
	}
	// But when nothing covers the agent, the actionable advice stays.
	if got := shimInstalledCheck(0, total, 0, dir, false); !strings.Contains(got.Detail, "ctx-wire init") {
		t.Errorf("unwired user should be pointed at init, got %q", got.Detail)
	}
}

// TestFormatThemedHidesOffByDefault pins the doctor collapse: Off rows are
// hidden by default behind a one-line count (only actionable state renders),
// sections that are nothing-but-off vanish entirely, and showAll restores the
// full report. Hiding must never change health.
func TestFormatThemedHidesOffByDefault(t *testing.T) {
	r := &Report{Sections: []Section{
		{Title: "binary", Checks: []Check{{"version", OK, "dev"}}},
		{Title: "hooks", Checks: []Check{
			{"claude", OK, "hook present"},
			{"cline", Off, "not configured (run `ctx-wire init cline` to enable)"},
		}},
		{Title: "mcp", Checks: []Check{
			{"vscode (workspace)", Off, "not configured"},
			{"visualstudio (user)", Off, "not configured"},
		}},
	}}
	theme := ui.Plain()

	def := FormatThemed(r, theme, false)
	for _, gone := range []string{"cline", "vscode (workspace)", "visualstudio (user)", "MCP"} {
		if strings.Contains(def, gone) {
			t.Errorf("default view must hide %q:\n%s", gone, def)
		}
	}
	if !strings.Contains(def, "3 optional check(s) hidden") || !strings.Contains(def, "doctor --all") {
		t.Errorf("default view must summarize hidden checks and point at --all:\n%s", def)
	}
	if !strings.Contains(def, "claude") || !strings.Contains(def, "healthy") {
		t.Errorf("default view lost actionable rows or health:\n%s", def)
	}

	all := FormatThemed(r, theme, true)
	for _, want := range []string{"cline", "vscode (workspace)", "MCP", "claude"} {
		if !strings.Contains(all, want) {
			t.Errorf("--all view must show %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "hidden") {
		t.Errorf("--all view must not print the hidden-count line:\n%s", all)
	}
}
