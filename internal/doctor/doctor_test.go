package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/install"
	"ctx-wire/internal/shim"
)

// isolate points HOME/XDG at temp dirs so doctor never reads the developer's
// real agent config or storage, and returns a fresh workdir.
func isolate(t *testing.T) (home, workdir string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CTX_WIRE_TELEMETRY", "")
	// Keep gain/tee writes inside the temp tree.
	t.Setenv("CTX_WIRE_GAIN_FILE", filepath.Join(home, "gain.jsonl"))
	t.Setenv("CTX_WIRE_TEE_DIR", filepath.Join(home, "tee"))
	return home, t.TempDir()
}

// findCheck returns the named check from a section title, or false.
func findCheck(r *Report, section, name string) (Check, bool) {
	for _, sec := range r.Sections {
		if sec.Title != section {
			continue
		}
		for _, c := range sec.Checks {
			if c.Name == name {
				return c, true
			}
		}
	}
	return Check{}, false
}

func TestRunCleanEnvIsHealthy(t *testing.T) {
	_, wd := isolate(t)
	r := Run(Options{Version: "test", Workdir: wd})
	if !r.Healthy() {
		t.Fatalf("expected healthy report in clean temp env:\n%s", Format(r))
	}
	// Storage must be writable (temp dirs), so those are OK not Fail.
	if c, ok := findCheck(r, "storage", "gain log"); !ok || c.Status != OK {
		t.Errorf("gain log check = %+v, want OK", c)
	}
	if c, ok := findCheck(r, "filters", "built-in registry"); !ok || c.Status != OK {
		t.Errorf("registry check = %+v, want OK", c)
	}
	if c, ok := findCheck(r, "telemetry", "status"); !ok || c.Status != OK || !strings.Contains(c.Detail, "enabled") {
		t.Errorf("telemetry check = %+v, want OK enabled (opt-out default)", c)
	}
}

func TestHooksDetected(t *testing.T) {
	home, wd := isolate(t)
	// Write a Claude settings.json that contains the ctx-wire hook.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"ctx-wire hook claude"}]}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Run(Options{Version: "test", Workdir: wd})
	c, ok := findCheck(r, "hooks", "claude")
	if !ok {
		t.Fatal("missing claude hook check")
	}
	if c.Status != OK || !strings.Contains(c.Detail, "present") {
		t.Errorf("claude hook check = %+v, want OK present", c)
	}
}

func TestHookMissingIsWarnNotFail(t *testing.T) {
	_, wd := isolate(t)
	r := Run(Options{Version: "test", Workdir: wd})
	if c, ok := findCheck(r, "hooks", "claude"); ok && c.Status == Fail {
		t.Errorf("missing claude hook should warn, not fail: %+v", c)
	}
	if !r.Healthy() {
		t.Errorf("missing hooks must not make doctor unhealthy:\n%s", Format(r))
	}
}

// TestHookOrPluginCoverageExcludesMCP guards that only hook/plugin wiring counts
// as shell-command coverage. An MCP server entry (.vscode/mcp.json) is a separate
// protocol, not a command rewrite, so an MCP-only setup must not make doctor
// report installed PATH shims as redundant. Regression for the WiringMCP probes
// added to AgentProbes when the MCP section moved onto the registry.
func TestHookOrPluginCoverageExcludesMCP(t *testing.T) {
	_, wd := isolate(t)
	opts := Options{Workdir: wd}

	if hookOrPluginConfigured(opts) {
		t.Fatal("clean env must report no hook/plugin coverage")
	}

	// A ctx-wire MCP server in the workspace must NOT count as command coverage.
	mcpPath := install.VSCodeMCPPath(wd)
	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcpPath, []byte(`{"servers":{"ctx-wire":{"command":"ctx-wire"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if hookOrPluginConfigured(opts) {
		t.Error("MCP-only config must not count as command coverage (would wrongly flag PATH shims as redundant)")
	}

	// A real hook (cursor) DOES count, so the predicate still detects coverage.
	cursorPath, err := install.CursorHooksPath()
	if err != nil {
		t.Fatalf("CursorHooksPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cursorPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorPath, []byte(`{"hooks":{"preToolUse":[{"command":"ctx-wire hook cursor"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hookOrPluginConfigured(opts) {
		t.Error("a configured hook must count as command coverage")
	}
}

func TestShimsDetected(t *testing.T) {
	_, wd := isolate(t)
	dest, err := install.SelfInstallPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := shim.Install(filepath.Dir(dest), dest, []string{"git"}); err != nil {
		t.Fatalf("install shim: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(dest))
	t.Setenv(shim.EnvLog, filepath.Join(filepath.Dir(dest), "shims.jsonl"))
	if err := shim.RecordUse("git", "git status --short"); err != nil {
		t.Fatalf("record shim use: %v", err)
	}

	r := Run(Options{Version: "test", Workdir: wd})
	if c, ok := findCheck(r, "shims", "PATH"); !ok || c.Status != OK {
		t.Fatalf("shim PATH check = %+v, want OK", c)
	}
	if c, ok := findCheck(r, "shims", "usage"); !ok || c.Status != OK || !strings.Contains(c.Detail, "1 shim capture") {
		t.Fatalf("shim usage check = %+v, want one capture", c)
	}
}

func TestUnwritableStorageNoFallbackFails(t *testing.T) {
	home, wd := isolate(t)
	// Point the gain path at a location whose ancestor is a file, so no dir under
	// it can be created. An explicit CTX_WIRE_GAIN_FILE disables the fallback.
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CTX_WIRE_GAIN_FILE", filepath.Join(blocker, "sub", "gain.jsonl"))

	r := Run(Options{Version: "test", Workdir: wd})
	if r.Healthy() {
		t.Errorf("report with no writable gain target must be unhealthy:\n%s", Format(r))
	}
	// Primary warns, fallback fails.
	if c, ok := findCheck(r, "storage", "gain log"); !ok || c.Status != Warn {
		t.Errorf("gain log primary = %+v, want Warn", c)
	}
	if c, ok := findCheck(r, "storage", "gain log fallback"); !ok || c.Status != Fail {
		t.Errorf("gain log fallback = %+v, want Fail", c)
	}
}

func TestStoragePrimaryUnwritableFallbackWarnsHealthy(t *testing.T) {
	home, wd := isolate(t)
	// Enable the fallback by clearing the explicit gain-file override.
	t.Setenv("CTX_WIRE_GAIN_FILE", "")
	// Make the primary (XDG) location unwritable: its data dir sits under a file.
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", blocker)
	// Point the fallback at a writable temp location.
	t.Setenv("CTX_WIRE_GAIN_FALLBACK_FILE", filepath.Join(t.TempDir(), "gain.jsonl"))

	r := Run(Options{Version: "test", Workdir: wd})
	if c, ok := findCheck(r, "storage", "gain log"); !ok || c.Status != Warn {
		t.Errorf("gain log primary = %+v, want Warn", c)
	}
	if c, ok := findCheck(r, "storage", "gain log fallback"); !ok || c.Status != OK {
		t.Errorf("gain log fallback = %+v, want OK", c)
	}
	if !r.Healthy() {
		t.Errorf("a writable fallback must keep doctor healthy:\n%s", Format(r))
	}
}

func TestProjectFilterTrustStates(t *testing.T) {
	_, wd := isolate(t)
	// No project filter -> OK "none".
	r := Run(Options{Version: "test", Workdir: wd})
	if c, ok := findCheck(r, "filters", "project filters"); !ok || c.Status != OK {
		t.Errorf("absent project filters = %+v, want OK", c)
	}

	// Untrusted project filter -> Warn.
	pdir := filepath.Join(wd, ".ctx-wire")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "filters.toml"), []byte("schema_version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = Run(Options{Version: "test", Workdir: wd})
	c, ok := findCheck(r, "filters", "project filters")
	if !ok || c.Status != Warn || !strings.Contains(c.Detail, "untrusted") {
		t.Errorf("untrusted project filters = %+v, want Warn untrusted", c)
	}
}

func TestRecentHiddenByDefault(t *testing.T) {
	home, wd := isolate(t)
	gainFile := filepath.Join(home, "gain.jsonl")
	line := `{"ts":"2026-05-29T10:00:00Z","command":"git status","mode":"filtered","raw_bytes":1000,"emitted_bytes":100,"saved_bytes":900,"exit_code":0}` + "\n"
	if err := os.WriteFile(gainFile, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	// Default: counts only, no command history.
	r := Run(Options{Version: "test", Workdir: wd})
	if strings.Contains(Format(r), "git status") {
		t.Errorf("default doctor must not print command history:\n%s", Format(r))
	}

	// --recent: includes the scrubbed command.
	r = Run(Options{Version: "test", Workdir: wd, Recent: 5})
	if !strings.Contains(Format(r), "git status") {
		t.Errorf("--recent should include recent command:\n%s", Format(r))
	}
}

// TestDoctorMultiConfigAllHooked verifies that when multiple Claude config dirs
// are present and all are hooked, the report shows OK for each dir.
func TestDoctorMultiConfigAllHooked(t *testing.T) {
	home, wd := isolate(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	settings := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"ctx-wire hook claude"}]}]}}`

	// Primary dir: ~/.claude
	primaryDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(primaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primaryDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	// Secondary dir: ~/.claude-main (real config, must have projects/).
	secondaryDir := filepath.Join(home, ".claude-main")
	if err := os.MkdirAll(filepath.Join(secondaryDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondaryDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Run(Options{Version: "test", Workdir: wd})

	// Both config dirs must show OK.
	for _, sec := range r.Sections {
		if sec.Title != "hooks" {
			continue
		}
		for _, c := range sec.Checks {
			if strings.Contains(c.Name, "claude config") {
				if c.Status != OK {
					t.Errorf("multi-config check %q = %v %q, want OK", c.Name, c.Status, c.Detail)
				}
			}
		}
	}
	if !r.Healthy() {
		t.Errorf("all configs hooked must be healthy:\n%s", Format(r))
	}
}

// TestDoctorMultiConfigOneUnhooked verifies that when one config dir has no
// hook, that check is Warn and the report is still healthy (Warn, not Fail).
func TestDoctorMultiConfigOneUnhooked(t *testing.T) {
	home, wd := isolate(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	hookedSettings := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"ctx-wire hook claude"}]}]}}`
	unhooked := `{}`

	// Primary: hooked.
	primaryDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(primaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primaryDir, "settings.json"), []byte(hookedSettings), 0o644); err != nil {
		t.Fatal(err)
	}

	// Secondary: settings.json present but NOT hooked.
	secondaryDir := filepath.Join(home, ".claude-ship")
	if err := os.MkdirAll(filepath.Join(secondaryDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondaryDir, "settings.json"), []byte(unhooked), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Run(Options{Version: "test", Workdir: wd})

	warnFound := false
	for _, sec := range r.Sections {
		if sec.Title != "hooks" {
			continue
		}
		for _, c := range sec.Checks {
			if strings.Contains(c.Name, "claude config") && c.Status == Warn {
				warnFound = true
			}
		}
	}
	if !warnFound {
		t.Errorf("expected a Warn for the unhooked config dir:\n%s", Format(r))
	}
	// Warn does not make doctor unhealthy.
	if !r.Healthy() {
		t.Errorf("unhooked config dir is a warning, not a failure; doctor should be healthy:\n%s", Format(r))
	}
}
