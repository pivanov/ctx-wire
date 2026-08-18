package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	winExe        = `C:\Users\User Name\AppData\Local\ctx-wire\bin\ctx-wire.exe`
	legacyCopilot = "ctx-wire hook copilot"
)

// pinWindowsHookCommand makes hookCommand produce the Windows forms for the rest
// of the test, so the upgrade path can be driven on any host.
func pinWindowsHookCommand(t *testing.T) {
	t.Helper()
	prev := hookCommand
	hookCommand = func(agent string) string { return hookCommandFor("windows", winExe, agent) }
	t.Cleanup(func() { hookCommand = prev })
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, data)
	}
	return root
}

// The upgrade that matters. Every installer decides "already wired?" with
// isHookCommand, which accepts the legacy bare form on purpose so an upgrade does
// not duplicate the entry. That same tolerance used to END the install: a config
// wired before 0.1.66 kept its bare, PATH-dependent command, `init` said "already
// configured", and no amount of reinstalling the binary could repair it. On
// Windows that is a hook the agent cannot spawn, and Copilot denies tool calls on
// a hook it cannot spawn.
func TestInstallCopilotSettingsUpgradesLegacyCommand(t *testing.T) {
	pinWindowsHookCommand(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	legacy := `{"hooks":{"preToolUse":[{"type":"command","command":"` + legacyCopilot + `"}]}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := InstallCopilotSettings(path)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !changed {
		t.Fatal("install reported no change; a legacy bare command was left in place and no reinstall can fix it")
	}

	pre := readJSON(t, path)["hooks"].(map[string]any)["preToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("want 1 entry after upgrade, got %d (the upgrade duplicated the hook)", len(pre))
	}
	entry := pre[0].(map[string]any)
	if got, want := entry["command"], hookCommand("copilot"); got != want {
		t.Errorf("command = %v, want %v", got, want)
	}
	if got, _ := entry["matcher"].(string); got != copilotShellMatcher {
		t.Errorf("matcher = %q, want %q: a legacy unscoped entry denies every tool when the hook cannot spawn", got, copilotShellMatcher)
	}
}

// Upgrading must be idempotent, or `init` reports a change on every run.
func TestInstallCopilotSettingsUpgradeIsIdempotent(t *testing.T) {
	pinWindowsHookCommand(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if _, err := InstallCopilotSettings(path); err != nil {
		t.Fatal(err)
	}
	changed, err := InstallCopilotSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second install reported a change; the upgrade is not idempotent")
	}
}

// An upgrade must never touch someone else's hook.
func TestInstallCopilotSettingsUpgradePreservesForeignEntries(t *testing.T) {
	pinWindowsHookCommand(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	legacy := `{"hooks":{"preToolUse":[` +
		`{"type":"command","command":"other-tool guard"},` +
		`{"type":"command","command":"` + legacyCopilot + `"}]}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCopilotSettings(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "other-tool guard") {
		t.Errorf("foreign hook lost during upgrade:\n%s", data)
	}
	pre := readJSON(t, path)["hooks"].(map[string]any)["preToolUse"].([]any)
	if len(pre) != 2 {
		t.Errorf("want 2 entries (foreign + ours), got %d:\n%s", len(pre), data)
	}
}

// Claude and Codex nest their command inside a per-matcher entry; Cursor is flat.
// All three had the same non-upgrading guard.
func TestInstallUpgradesLegacyCommandForEveryAgent(t *testing.T) {
	cases := []struct {
		agent    string
		legacy   string
		install  func(path string) (bool, error)
		commands func(t *testing.T, path string) []string
	}{
		{
			agent:   "claude",
			legacy:  `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"ctx-wire hook claude"}]}]}}`,
			install: InstallClaude,
			commands: func(t *testing.T, path string) []string {
				return nestedCommands(t, path, "hooks", "PreToolUse")
			},
		},
		{
			agent:   "codex",
			legacy:  `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"ctx-wire hook codex"}]}],"PermissionRequest":[{"matcher":"Bash","hooks":[{"type":"command","command":"ctx-wire hook codex"}]}]}}`,
			install: InstallCodexHooks,
			commands: func(t *testing.T, path string) []string {
				return nestedCommands(t, path, "hooks", "PreToolUse")
			},
		},
		{
			agent:   "cursor",
			legacy:  `{"version":1,"hooks":{"preToolUse":[{"matcher":"Shell","command":"ctx-wire hook cursor"}]}}`,
			install: InstallCursor,
			commands: func(t *testing.T, path string) []string {
				root := readJSON(t, path)
				var out []string
				for _, e := range root["hooks"].(map[string]any)["preToolUse"].([]any) {
					cmd, _ := e.(map[string]any)["command"].(string)
					out = append(out, cmd)
				}
				return out
			},
		},
	}
	for _, c := range cases {
		t.Run(c.agent, func(t *testing.T) {
			pinWindowsHookCommand(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte(c.legacy), 0o644); err != nil {
				t.Fatal(err)
			}
			changed, err := c.install(path)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if !changed {
				t.Fatal("install reported no change; the legacy bare command survives and no reinstall can fix it")
			}
			cmds := c.commands(t, path)
			if len(cmds) != 1 {
				t.Fatalf("want 1 ctx-wire command after upgrade, got %d: %q", len(cmds), cmds)
			}
			if want := hookCommand(c.agent); cmds[0] != want {
				t.Errorf("command = %q, want %q", cmds[0], want)
			}
			// A second run must settle.
			if changed, err := c.install(path); err != nil || changed {
				t.Errorf("second install: changed=%v err=%v, want false/nil", changed, err)
			}
		})
	}
}

func nestedCommands(t *testing.T, path, objectKey, eventKey string) []string {
	t.Helper()
	root := readJSON(t, path)
	parent, _ := root[objectKey].(map[string]any)
	var out []string
	for _, e := range parent[eventKey].([]any) {
		inner, _ := e.(map[string]any)["hooks"].([]any)
		for _, h := range inner {
			if cmd, _ := h.(map[string]any)["command"].(string); cmd != "" {
				out = append(out, cmd)
			}
		}
	}
	return out
}
