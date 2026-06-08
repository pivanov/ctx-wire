package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPWrapInstallRoundTrip(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "claude.json")
	const orig = `{
  "projects": {
    "/p": { "mcpServers": { "cdt": {"type":"stdio","command":"npx","args":["chrome-devtools-mcp@latest"],"env":{}} } }
  },
  "mcpServers": { "other": {"command":"node","args":["x.js"]} },
  "unrelatedSetting": 42
}`
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	cdtArgs := func() []any {
		t.Helper()
		var m map[string]any
		data, _ := os.ReadFile(cfg)
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("config no longer parses: %v", err)
		}
		// unrelated data must survive every rewrite.
		if m["unrelatedSetting"].(float64) != 42 {
			t.Error("an unrelated config key was lost")
		}
		if m["mcpServers"].(map[string]any)["other"].(map[string]any)["command"] != "node" {
			t.Error("an unrelated mcpServers entry was modified")
		}
		return m["projects"].(map[string]any)["/p"].(map[string]any)["mcpServers"].(map[string]any)["cdt"].(map[string]any)["args"].([]any)
	}

	if code := mcpWrapInstall(cfg, "cdt"); code != 0 {
		t.Fatalf("install exit %d", code)
	}
	args := cdtArgs()
	if len(args) != 4 || args[0] != "mcp-wrap" || args[1] != "--" || args[2] != "npx" || args[3] != "chrome-devtools-mcp@latest" {
		t.Fatalf("wrapped args = %v, want [mcp-wrap -- npx chrome-devtools-mcp@latest]", args)
	}

	// Idempotent: a second install does not double-wrap.
	if code := mcpWrapInstall(cfg, "cdt"); code != 0 {
		t.Fatalf("second install exit %d", code)
	}
	if a := cdtArgs(); len(a) != 4 {
		t.Errorf("second install double-wrapped: %v", a)
	}

	// Uninstall restores the original command and args exactly.
	if code := mcpWrapUninstall(cfg, "cdt"); code != 0 {
		t.Fatalf("uninstall exit %d", code)
	}
	a := cdtArgs()
	if len(a) != 1 || a[0] != "chrome-devtools-mcp@latest" {
		t.Errorf("uninstall args = %v, want [chrome-devtools-mcp@latest]", a)
	}
	var m map[string]any
	data, _ := os.ReadFile(cfg)
	json.Unmarshal(data, &m)
	if cmd := m["projects"].(map[string]any)["/p"].(map[string]any)["mcpServers"].(map[string]any)["cdt"].(map[string]any)["command"]; cmd != "npx" {
		t.Errorf("uninstall restored command = %v, want npx", cmd)
	}
	if _, err := os.Stat(cfg + ".ctxw-bak"); err != nil {
		t.Error("expected a backup file")
	}
}
