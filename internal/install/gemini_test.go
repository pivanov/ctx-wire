package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallGeminiHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks", "ctx-wire-hook-gemini.sh")
	changed, err := InstallGeminiHook(path)
	if err != nil {
		t.Fatalf("InstallGeminiHook: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != geminiHookScript {
		t.Fatalf("hook script = %q", got)
	}
	if mode := mustStat(t, path).Mode().Perm(); mode != 0o755 {
		t.Fatalf("mode = %o, want 755", mode)
	}
}

func TestInstallGeminiSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	hookPath := filepath.Join(t.TempDir(), "ctx-wire-hook-gemini.sh")
	changed, err := InstallGeminiSettings(path, hookPath)
	if err != nil {
		t.Fatalf("InstallGeminiSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	root := readSettings(t, path)
	before := root["hooks"].(map[string]any)["BeforeTool"].([]any)
	if len(before) != 1 {
		t.Fatalf("BeforeTool entries = %d", len(before))
	}
	if !containsHookEntry(before, geminiHookEntry(hookPath)) {
		t.Fatal("Gemini hook entry missing")
	}
}

func TestInstallGeminiSettingsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	hookPath := filepath.Join(t.TempDir(), "ctx-wire-hook-gemini.sh")
	if _, err := InstallGeminiSettings(path, hookPath); err != nil {
		t.Fatalf("first install: %v", err)
	}
	changed, err := InstallGeminiSettings(path, hookPath)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false")
	}
}

func TestInstallGeminiSettingsPreservesExistingHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	hookPath := filepath.Join(t.TempDir(), "ctx-wire-hook-gemini.sh")
	existing := `{"theme":"dark","hooks":{"BeforeTool":[{"matcher":"Read","hooks":[{"type":"command","command":"echo keep"}]}]}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := InstallGeminiSettings(path, hookPath)
	if err != nil {
		t.Fatalf("InstallGeminiSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "echo keep") || !strings.Contains(got, "ctx-wire-hook") {
		t.Fatalf("settings not merged:\n%s", got)
	}
}

func TestInstallGeminiSettingsRejectsMalformedHookShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"BeforeTool":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallGeminiSettings(path, filepath.Join(t.TempDir(), "hook.sh")); err == nil {
		t.Fatal("expected malformed BeforeTool error")
	}
}
