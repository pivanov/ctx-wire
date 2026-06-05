package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallClaudeFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	changed, err := InstallClaude(path)
	if err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}
	if !changed {
		t.Error("expected changed=true for fresh install")
	}
	if !hookPresent(t, path) {
		t.Error("hook not present after install")
	}
}

func TestInstallClaudeIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := InstallClaude(path); err != nil {
		t.Fatalf("first install: %v", err)
	}
	changed, err := InstallClaude(path)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Error("expected changed=false on second install (idempotent)")
	}
	// Exactly one PreToolUse entry.
	root := readSettings(t, path)
	hooks := root["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Errorf("expected 1 PreToolUse entry, got %d", len(pre))
	}
}

func TestInstallClaudePreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	existing := `{
      "model": "opus",
      "hooks": { "PreToolUse": [ { "matcher": "Read", "hooks": [ { "type": "command", "command": "other-tool" } ] } ] }
    }`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallClaude(path); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}
	root := readSettings(t, path)
	if root["model"] != "opus" {
		t.Errorf("existing setting 'model' lost: %v", root["model"])
	}
	pre := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Errorf("expected 2 PreToolUse entries (existing + ours), got %d", len(pre))
	}
	if !hookPresent(t, path) {
		t.Error("ctx-wire hook not added alongside existing hook")
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("expected a .bak backup of the previous settings")
	}
}

func TestInstallClaudeRejectsWrongHookShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"PreToolUse":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallClaude(path); err == nil {
		t.Fatal("expected schema error for non-array PreToolUse")
	}
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("settings not valid JSON: %v", err)
	}
	return root
}

func hookPresent(t *testing.T, path string) bool {
	t.Helper()
	root := readSettings(t, path)
	hooks, _ := root["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	return hasClaudeHook(pre)
}
