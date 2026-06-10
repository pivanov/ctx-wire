package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeMatchers(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // uninstall removes an empty settings file entirely
	}
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("settings no longer parse: %v\n%s", err, data)
	}
	var out []string
	for _, e := range root.Hooks.PreToolUse {
		for _, h := range e.Hooks {
			if h.Command == claudeHookCommand {
				out = append(out, e.Matcher)
			}
		}
	}
	return out
}

// TestClaudeFileToolsUpgradeMatrix runs the prior-state matrix: fresh install,
// Bash-only existing, both existing, and on->off->on transitions. The upgrade
// trap this pins: presence is (matcher, command), never command alone, so an
// existing Bash-only install still gains the Read|Grep entry.
func TestClaudeFileToolsUpgradeMatrix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	// Fresh: Bash install then file-tools install -> both matchers.
	if changed, err := InstallClaude(path); err != nil || !changed {
		t.Fatalf("InstallClaude fresh = %v, %v", changed, err)
	}
	if changed, err := InstallClaudeFileTools(path); err != nil || !changed {
		t.Fatalf("InstallClaudeFileTools on Bash-only = %v, %v (the upgrade trap)", changed, err)
	}
	got := claudeMatchers(t, path)
	if len(got) != 2 || got[0] != "Bash" || got[1] != claudeFileToolsMatcher {
		t.Fatalf("matchers = %v, want [Bash %s]", got, claudeFileToolsMatcher)
	}

	// Idempotent in both directions.
	if changed, _ := InstallClaude(path); changed {
		t.Error("InstallClaude must be idempotent with both matchers present")
	}
	if changed, _ := InstallClaudeFileTools(path); changed {
		t.Error("InstallClaudeFileTools must be idempotent")
	}

	// Off: removes exactly the Read|Grep entry, Bash survives.
	if changed, err := UninstallClaudeFileTools(path); err != nil || !changed {
		t.Fatalf("UninstallClaudeFileTools = %v, %v", changed, err)
	}
	if got := claudeMatchers(t, path); len(got) != 1 || got[0] != "Bash" {
		t.Fatalf("after off: matchers = %v, want [Bash]", got)
	}
	if changed, _ := UninstallClaudeFileTools(path); changed {
		t.Error("second off must be a no-op")
	}

	// On again.
	if changed, err := InstallClaudeFileTools(path); err != nil || !changed {
		t.Fatalf("re-enable = %v, %v", changed, err)
	}
	if got := claudeMatchers(t, path); len(got) != 2 {
		t.Fatalf("after re-enable: matchers = %v", got)
	}
}

// TestClaudeFileToolsPreservesForeignEntries pins that a foreign hook with the
// SAME matcher is never ours to remove.
func TestClaudeFileToolsPreservesForeignEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Read|Grep", "hooks": [{"type": "command", "command": "other-tool hook"}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "` + claudeHookCommand + `"}]}
    ]
  },
  "permissions": {"deny": ["Bash(rm:*)"]}
}`
	if err := os.WriteFile(path, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	// Install adds OUR entry alongside the foreign same-matcher one.
	if changed, err := InstallClaudeFileTools(path); err != nil || !changed {
		t.Fatalf("install = %v, %v", changed, err)
	}
	// Uninstall removes only ours; the foreign entry and permissions survive.
	if changed, err := UninstallClaudeFileTools(path); err != nil || !changed {
		t.Fatalf("uninstall = %v, %v", changed, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "other-tool hook") {
		t.Error("foreign same-matcher entry was removed")
	}
	if !strings.Contains(string(data), `"deny"`) {
		t.Error("unrelated settings lost")
	}
}

// TestUninstallClaudeRemovesAllMatchers pins that the full uninstall removes
// both ctx-wire entries (it removes by command, matcher-agnostic).
func TestUninstallClaudeRemovesAllMatchers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := InstallClaude(path); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallClaudeFileTools(path); err != nil {
		t.Fatal(err)
	}
	if changed, err := UninstallClaude(path); err != nil || !changed {
		t.Fatalf("UninstallClaude = %v, %v", changed, err)
	}
	if got := claudeMatchers(t, path); len(got) != 0 {
		t.Fatalf("full uninstall left entries: %v", got)
	}
}
