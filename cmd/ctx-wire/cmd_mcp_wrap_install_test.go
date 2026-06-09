package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMCPWrapCompressWrapUnwrap covers the --compress wrap shape and that both the
// new (`mcp-wrap --compress --`) and old (`mcp-wrap --`) shapes unwrap cleanly, so
// turning on compression stays reversible and an upgrade does not strand a server.
func TestMCPWrapCompressWrapUnwrap(t *testing.T) {
	const exe = "/path/to/ctx-wire"

	sc := map[string]any{"command": "npx", "args": []any{"chrome-devtools-mcp@latest"}}
	if !wrapServerEntry(sc, exe, true) {
		t.Fatal("expected --compress wrap to apply")
	}
	want := []any{"mcp-wrap", "--compress", "--", "npx", "chrome-devtools-mcp@latest"}
	if got := sc["args"].([]any); !reflect.DeepEqual(got, want) {
		t.Fatalf("compress wrap args = %v, want %v", got, want)
	}
	if sc["command"] != exe || !isWrapped(sc, exe) {
		t.Error("isWrapped must recognize the --compress shape")
	}
	if !unwrapServerEntry(sc, exe) {
		t.Fatal("expected unwrap to apply")
	}
	if sc["command"] != "npx" || !reflect.DeepEqual(sc["args"].([]any), []any{"chrome-devtools-mcp@latest"}) {
		t.Errorf("unwrap of --compress shape did not restore original: cmd=%v args=%v", sc["command"], sc["args"])
	}

	// Back-compat: a server wrapped the old measurement-only way must still unwrap.
	old := map[string]any{"command": exe, "args": []any{"mcp-wrap", "--", "node", "x.js"}}
	if !isWrapped(old, exe) {
		t.Error("isWrapped must still recognize the old `mcp-wrap --` shape")
	}
	if !unwrapServerEntry(old, exe) || old["command"] != "node" {
		t.Errorf("old shape did not unwrap to node: cmd=%v", old["command"])
	}
}

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

	if code := mcpWrapInstall(cfg, "cdt", false); code != 0 {
		t.Fatalf("install exit %d", code)
	}
	args := cdtArgs()
	if len(args) != 4 || args[0] != "mcp-wrap" || args[1] != "--" || args[2] != "npx" || args[3] != "chrome-devtools-mcp@latest" {
		t.Fatalf("wrapped args = %v, want [mcp-wrap -- npx chrome-devtools-mcp@latest]", args)
	}

	// Idempotent: a second install does not double-wrap.
	if code := mcpWrapInstall(cfg, "cdt", false); code != 0 {
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

func TestMCPWrapUninstallIgnoresForeignServer(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "claude.json")
	// A server the user configured themselves that happens to pass `mcp-wrap --`
	// to some other program. Its command is not ctx-wire, so uninstall must leave
	// it untouched rather than corrupting it.
	const orig = `{
  "mcpServers": {
    "mine": {"command":"node","args":["mcp-wrap","--","srv.js"]}
  }
}`
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := mcpWrapUninstall(cfg, "mine"); code != 0 {
		t.Fatalf("uninstall exit %d", code)
	}
	data, _ := os.ReadFile(cfg)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	sc := m["mcpServers"].(map[string]any)["mine"].(map[string]any)
	if sc["command"] != "node" {
		t.Errorf("foreign server command was rewritten to %v, want node", sc["command"])
	}
	if args := sc["args"].([]any); len(args) != 3 || args[0] != "mcp-wrap" || args[2] != "srv.js" {
		t.Errorf("foreign server args were modified: %v", args)
	}
}

func TestMCPWrapInstallPreservesKeyOrder(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "claude.json")
	// Keys are deliberately out of alphabetical order. A whole-file re-serialize
	// would sort them (alpha, mcpServers, zeta); the surgical splice must not.
	const orig = `{
  "zeta": 1,
  "mcpServers": {
    "cdt": {"type":"stdio","command":"npx","args":["chrome-devtools-mcp@latest"]}
  },
  "alpha": 2
}`
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := mcpWrapInstall(cfg, "cdt", false); code != 0 {
		t.Fatalf("install exit %d", code)
	}
	s, _ := os.ReadFile(cfg)
	zi := strings.Index(string(s), `"zeta"`)
	mi := strings.Index(string(s), `"mcpServers"`)
	ai := strings.Index(string(s), `"alpha"`)
	if !(zi >= 0 && zi < mi && mi < ai) {
		t.Errorf("key order not preserved: zeta=%d mcpServers=%d alpha=%d\n%s", zi, mi, ai, s)
	}
	if !strings.Contains(string(s), `"zeta": 1`) || !strings.Contains(string(s), `"alpha": 2`) {
		t.Errorf("unrelated keys were altered:\n%s", s)
	}
	var m map[string]any
	if err := json.Unmarshal(s, &m); err != nil {
		t.Fatalf("config no longer parses: %v", err)
	}
	if args := m["mcpServers"].(map[string]any)["cdt"].(map[string]any)["args"].([]any); len(args) != 4 || args[0] != "mcp-wrap" {
		t.Errorf("entry was not wrapped: %v", args)
	}
}
