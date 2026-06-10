package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The auto-wrap prior-state matrix. The dangerous bugs in this family live in
// transitions from states the happy path never sees (the `install --compress`
// no-op bug was exactly this), so every prior state is enumerated up front:
// fresh, measurement-wrapped, compress-wrapped, foreign wrapper, stale-binary
// wrap, http remote, command-only, project-level, unrecognized shape.

// autowrapExe returns this test binary's resolved path, which is what
// wrapServerEntry records as the relay command.
func autowrapExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return exe
}

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readCfg(t *testing.T, p string) map[string]any {
	t.Helper()
	var m map[string]any
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("config no longer parses: %v", err)
	}
	return m
}

func serverArgs(t *testing.T, m map[string]any, name string) []string {
	t.Helper()
	sc := m["mcpServers"].(map[string]any)[name].(map[string]any)
	return toStringList(sc["args"])
}

func TestAutoWrapPriorStateMatrix(t *testing.T) {
	exe := autowrapExe(t)
	cfg := writeCfg(t, `{
  "mcpServers": {
    "chrome-devtools": {"command":"npx","args":["chrome-devtools-mcp@latest"]},
    "playwright": {"command":"npx","args":["@playwright/mcp@latest","--isolated"]},
    "measured": {"command":"`+exe+`","args":["mcp-wrap","--","npx","chrome-devtools-mcp@latest"]},
    "compressed": {"command":"`+exe+`","args":["mcp-wrap","--compress","--","npx","@playwright/mcp@latest"]},
    "foreign-wrapper": {"command":"node","args":["mcp-wrap","--","srv.js"]},
    "stale-binary": {"command":"/old/path/ctx-wire","args":["mcp-wrap","--","npx","chrome-devtools-mcp@latest"]},
    "http-remote": {"type":"http","url":"https://mcp.example.test/sse"},
    "command-only": {"command":"chrome-devtools-mcp"},
    "unrelated": {"command":"node","args":["x.js"]},
    "weird-shape": "just a string"
  },
  "projects": {
    "/p": {"mcpServers": {"proj-cdt": {"command":"npx","args":["chrome-devtools-mcp@latest"]}}}
  },
  "unrelated_top": 42
}`)

	wrapped, err := autoWrapSnapshotMCP(cfg)
	if err != nil {
		t.Fatalf("autoWrapSnapshotMCP: %v", err)
	}

	want := map[string]bool{
		"chrome-devtools": true, // fresh -> wrapped
		"playwright":      true, // fresh, args carry the marker -> wrapped
		"measured":        true, // measurement wrap -> UPGRADED in place
		"command-only":    true, // no args, command carries the marker -> wrapped
		"proj-cdt":        true, // project-level entry -> wrapped
	}
	got := map[string]bool{}
	for _, n := range wrapped {
		got[n] = true
	}
	for n := range want {
		if !got[n] {
			t.Errorf("expected %q to be wrapped; wrapped = %v", n, wrapped)
		}
	}
	for _, frozen := range []string{"compressed", "foreign-wrapper", "stale-binary", "http-remote", "unrelated", "weird-shape"} {
		if got[frozen] {
			t.Errorf("%q must NOT be touched (prior state demands hands off)", frozen)
		}
	}

	m := readCfg(t, cfg)

	// Fresh wrap shape.
	if a := serverArgs(t, m, "chrome-devtools"); len(a) < 4 || a[0] != "mcp-wrap" || a[1] != "--compress" || a[2] != "--" {
		t.Errorf("chrome-devtools wrap shape = %v", a)
	}
	// Measurement wrap upgraded, not double-wrapped.
	if a := serverArgs(t, m, "measured"); strings.Join(a, " ") != "mcp-wrap --compress -- npx chrome-devtools-mcp@latest" {
		t.Errorf("measured upgrade = %v, want the in-place --compress upgrade", a)
	}
	// Foreign wrapper untouched byte-for-byte semantics.
	if sc := m["mcpServers"].(map[string]any)["foreign-wrapper"].(map[string]any); sc["command"] != "node" {
		t.Errorf("foreign wrapper command rewritten to %v", sc["command"])
	}
	// Stale ctx-wire wrap left for doctor, not chained.
	if sc := m["mcpServers"].(map[string]any)["stale-binary"].(map[string]any); sc["command"] != "/old/path/ctx-wire" {
		t.Errorf("stale-binary wrap was modified: %v", sc["command"])
	}
	// http remote untouched.
	if sc := m["mcpServers"].(map[string]any)["http-remote"].(map[string]any); sc["url"] != "https://mcp.example.test/sse" {
		t.Errorf("http remote was modified: %v", sc)
	}
	// Unrelated data preserved.
	if m["unrelated_top"].(float64) != 42 {
		t.Error("unrelated top-level key lost")
	}
	// Backup written.
	if _, err := os.Stat(cfg + ".ctxw-bak"); err != nil {
		t.Error("expected a pristine backup")
	}

	// Idempotence: a second auto-wrap changes nothing.
	again, err := autoWrapSnapshotMCP(cfg)
	if err != nil {
		t.Fatalf("second autoWrap: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("auto-wrap not idempotent; second pass changed %v", again)
	}
}

// TestUnwrapAllRevertsEverything is the uninstall hard requirement: every wrap
// THIS binary made (fresh or upgraded) is reverted to the original launch, and
// foreign/stale entries stay untouched.
func TestUnwrapAllRevertsEverything(t *testing.T) {
	cfg := writeCfg(t, `{
  "mcpServers": {
    "chrome-devtools": {"command":"npx","args":["chrome-devtools-mcp@latest"]},
    "foreign-wrapper": {"command":"node","args":["mcp-wrap","--","srv.js"]}
  },
  "projects": {
    "/p": {"mcpServers": {"proj-cdt": {"command":"npx","args":["chrome-devtools-mcp@latest"]}}}
  }
}`)
	if _, err := autoWrapSnapshotMCP(cfg); err != nil {
		t.Fatal(err)
	}

	unwrapped, err := unwrapAllCtxWireMCP(cfg)
	if err != nil {
		t.Fatalf("unwrapAllCtxWireMCP: %v", err)
	}
	got := map[string]bool{}
	for _, n := range unwrapped {
		got[n] = true
	}
	if !got["chrome-devtools"] || !got["proj-cdt"] {
		t.Errorf("expected both wrapped servers reverted; got %v", unwrapped)
	}
	if got["foreign-wrapper"] {
		t.Error("uninstall must never rewrite a foreign mcp-wrap lookalike")
	}

	m := readCfg(t, cfg)
	if sc := m["mcpServers"].(map[string]any)["chrome-devtools"].(map[string]any); sc["command"] != "npx" {
		t.Errorf("chrome-devtools not restored: command = %v", sc["command"])
	}
	if a := serverArgs(t, m, "chrome-devtools"); len(a) != 1 || a[0] != "chrome-devtools-mcp@latest" {
		t.Errorf("chrome-devtools args not restored: %v", a)
	}
	proj := m["projects"].(map[string]any)["/p"].(map[string]any)["mcpServers"].(map[string]any)["proj-cdt"].(map[string]any)
	if proj["command"] != "npx" {
		t.Errorf("project-level entry not restored: %v", proj["command"])
	}

	// Idempotence: nothing left to unwrap.
	again, err := unwrapAllCtxWireMCP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("second unwrap changed %v, want nothing", again)
	}
}

// TestAutoWrapMissingConfigIsNoop: a machine without the agent config simply
// has nothing to wrap; init must not fail on it.
func TestAutoWrapMissingConfigIsNoop(t *testing.T) {
	wrapped, err := autoWrapSnapshotMCP(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || wrapped != nil {
		t.Errorf("missing config: wrapped=%v err=%v, want nil/nil", wrapped, err)
	}
}
