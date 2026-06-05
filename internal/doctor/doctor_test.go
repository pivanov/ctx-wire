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
		t.Errorf("telemetry check = %+v, want OK enabled", c)
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
