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
