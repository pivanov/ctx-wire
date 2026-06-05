package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallCursorGoldenFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	changed, err := InstallCursor(path)
	if err != nil {
		t.Fatalf("InstallCursor: %v", err)
	}
	if !changed {
		t.Error("expected changed=true for fresh install")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/cursor_hooks.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch\n got:\n%s\n want:\n%s", got, want)
	}
}

func TestInstallCursorIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := InstallCursor(path); err != nil {
		t.Fatalf("first install: %v", err)
	}
	changed, err := InstallCursor(path)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Error("expected changed=false on second install")
	}
	root := readSettings(t, path)
	pre := root["hooks"].(map[string]any)["preToolUse"].([]any)
	if len(pre) != 1 {
		t.Errorf("expected 1 preToolUse entry, got %d", len(pre))
	}
}

func TestInstallCursorPreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{
      "version": 1,
      "hooks": {
        "preToolUse": [ { "command": "other-tool", "matcher": "Shell" } ],
        "afterFileEdit": [ { "command": "fmt-tool" } ]
      }
    }`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCursor(path); err != nil {
		t.Fatalf("InstallCursor: %v", err)
	}
	root := readSettings(t, path)
	hooks := root["hooks"].(map[string]any)
	if _, ok := hooks["afterFileEdit"]; !ok {
		t.Error("existing afterFileEdit hook lost")
	}
	pre := hooks["preToolUse"].([]any)
	if len(pre) != 2 {
		t.Errorf("expected 2 preToolUse entries (existing + ours), got %d", len(pre))
	}
	if !hasCursorHook(pre) {
		t.Error("ctx-wire cursor hook not added")
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("expected a .bak backup")
	}
}

func TestInstallCursorRejectsWrongHookShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"preToolUse":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCursor(path); err == nil {
		t.Fatal("expected schema error for non-array preToolUse")
	}
}
