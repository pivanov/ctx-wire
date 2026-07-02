package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallClineRulesPreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".clinerules")
	if err := os.WriteFile(path, []byte("# Existing\n\nKeep me.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := InstallCline(path)
	if err != nil {
		t.Fatalf("InstallCline: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "Keep me.") || !strings.Contains(got, ctxWireBlockStart) {
		t.Fatalf("rules not merged:\n%s", got)
	}
}

func TestInstallWindsurfRulesIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".windsurfrules")
	if _, err := InstallWindsurf(path); err != nil {
		t.Fatalf("first install: %v", err)
	}
	changed, err := InstallWindsurf(path)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false")
	}
}

func TestInstallNestedRulesCreatesDirs(t *testing.T) {
	wd := t.TempDir()
	cases := []struct {
		name string
		path string
		do   func(string) (bool, error)
	}{
		{"kilocode", KilocodeRulesPath(wd), InstallKilocode},
		{"antigravity", AntigravityRulesPath(wd), InstallAntigravity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			changed, err := c.do(c.path) // parent dir does not exist yet
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if !changed {
				t.Fatal("expected changed=true on first install")
			}
			data, err := os.ReadFile(c.path)
			if err != nil {
				t.Fatalf("rules file not written: %v", err)
			}
			if !strings.Contains(string(data), ctxWireBlockStart) {
				t.Errorf("rules block missing:\n%s", data)
			}
			// Idempotent.
			if changed, err := c.do(c.path); err != nil || changed {
				t.Errorf("second install: changed=%v err=%v, want (false, nil)", changed, err)
			}
		})
	}
}

func TestInstallCopilot(t *testing.T) {
	dir := t.TempDir()
	changed, err := InstallCopilot(CopilotInstructionsPath(dir), CopilotHookPath(dir))
	if err != nil {
		t.Fatalf("InstallCopilot: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if data, err := os.ReadFile(CopilotInstructionsPath(dir)); err != nil || !strings.Contains(string(data), "ctx-wire run git status") {
		t.Fatalf("instructions missing: %v %q", err, data)
	}
	if data, err := os.ReadFile(CopilotHookPath(dir)); err != nil || !strings.Contains(string(data), "ctx-wire hook copilot") {
		t.Fatalf("hook missing: %v %q", err, data)
	}
}

func TestInstallCopilotSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","hooks":{"preToolUse":[{"type":"command","bash":"echo keep"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := InstallCopilotSettings(path)
	if err != nil {
		t.Fatalf("InstallCopilotSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{`"theme": "dark"`, `"bash": "echo keep"`, `"command": "ctx-wire hook copilot"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("settings missing %q:\n%s", want, content)
		}
	}

	changed, err = InstallCopilotSettings(path)
	if err != nil {
		t.Fatalf("second InstallCopilotSettings: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false on second install")
	}
}

func TestInstallCopilotSettingsRepairsBrokenBashEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"preToolUse":[{"type":"command","bash":"ctx-wire hook copilot"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := InstallCopilotSettings(path)
	if err != nil {
		t.Fatalf("InstallCopilotSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true so the live command hook is added")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"command": "ctx-wire hook copilot"`) {
		t.Fatalf("command hook missing after repair:\n%s", data)
	}
}

func TestInstallCopilotIdempotent(t *testing.T) {
	dir := t.TempDir()
	instrPath := CopilotInstructionsPath(dir)
	hookPath := CopilotHookPath(dir)

	if _, err := InstallCopilot(instrPath, hookPath); err != nil {
		t.Fatalf("first InstallCopilot: %v", err)
	}

	changed, err := InstallCopilot(instrPath, hookPath)
	if err != nil {
		t.Fatalf("second InstallCopilot: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false on second install (idempotency failure)")
	}

	// Exactly one ctx-wire block must be present in the instructions file.
	data, err := os.ReadFile(instrPath)
	if err != nil {
		t.Fatalf("read instructions: %v", err)
	}
	count := strings.Count(string(data), ctxWireBlockStart)
	if count != 1 {
		t.Fatalf("instructions file contains %d ctx-wire block(s), want exactly 1:\n%s", count, data)
	}

	// The hook file must still contain exactly the managed JSON.
	hookData, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if string(hookData) != copilotHookJSON {
		t.Fatalf("hook file changed on second install:\ngot:  %q\nwant: %q", hookData, copilotHookJSON)
	}
}
