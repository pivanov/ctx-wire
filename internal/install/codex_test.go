package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallCodexHooksGoldenFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	changed, err := InstallCodexHooks(path)
	if err != nil {
		t.Fatalf("InstallCodexHooks: %v", err)
	}
	if !changed {
		t.Error("expected changed=true for fresh install")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/codex_hooks.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch\n got:\n%s\n want:\n%s", got, want)
	}
}

func TestInstallCodexHooksIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := InstallCodexHooks(path); err != nil {
		t.Fatalf("first install: %v", err)
	}
	changed, err := InstallCodexHooks(path)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Error("expected changed=false on second install")
	}
}

func TestInstallCodexHooksPreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"other"}]}]}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexHooks(path); err != nil {
		t.Fatalf("InstallCodexHooks: %v", err)
	}
	root := readSettings(t, path)
	pre := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Errorf("expected 2 PreToolUse entries (existing + ours), got %d", len(pre))
	}
	if !hasCodexHook(pre) {
		t.Error("ctx-wire codex PreToolUse hook not added")
	}
	perm := root["hooks"].(map[string]any)["PermissionRequest"].([]any)
	if len(perm) != 1 {
		t.Errorf("expected 1 PermissionRequest entry, got %d", len(perm))
	}
	if !hasCodexHook(perm) {
		t.Error("ctx-wire codex PermissionRequest hook not added")
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("expected a .bak backup")
	}
}

func TestInstallCodexHooksAddsMissingPermissionRequestToExistingPreToolUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"ctx-wire hook codex"}]}]}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := InstallCodexHooks(path)
	if err != nil {
		t.Fatalf("InstallCodexHooks: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when PermissionRequest hook is missing")
	}
	root := readSettings(t, path)
	hooks := root["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Errorf("expected existing PreToolUse hook to remain single, got %d", len(pre))
	}
	perm := hooks["PermissionRequest"].([]any)
	if len(perm) != 1 || !hasCodexHook(perm) {
		t.Fatalf("PermissionRequest hook not installed correctly: %#v", perm)
	}
}

func TestInstallCodexHooksRejectsWrongHookShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"PreToolUse":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexHooks(path); err == nil {
		t.Fatal("expected schema error for non-array PreToolUse")
	}
}

func TestCodexHooksEnabled(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"missing file", "", false},
		{"feature off", "[features]\nhooks = false\n", false},
		{"no features table", "model = \"gpt-5.5\"\n", false},
		{"feature on", "[features]\nhooks = true\n", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".toml")
			if tt.content != "" {
				if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := CodexHooksEnabled(path)
			if err != nil {
				t.Fatalf("CodexHooksEnabled: %v", err)
			}
			if got != tt.want {
				t.Errorf("CodexHooksEnabled = %v, want %v", got, tt.want)
			}
		})
	}
}
