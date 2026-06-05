package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveFileIfContent(t *testing.T) {
	dir := t.TempDir()

	ours := filepath.Join(dir, "ours.ts")
	if err := os.WriteFile(ours, []byte(opencodePlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if !removeFileIfContent(ours, opencodePlugin) {
		t.Error("should remove a file whose content matches ours")
	}
	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Error("file should be gone")
	}

	// A user-owned file at the same path (different content) is left intact.
	user := filepath.Join(dir, "user.ts")
	if err := os.WriteFile(user, []byte("// my own plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removeFileIfContent(user, opencodePlugin) {
		t.Error("must not remove a file that isn't ours")
	}
	if _, err := os.Stat(user); err != nil {
		t.Error("user file should still exist")
	}

	// Missing file is a no-op.
	if removeFileIfContent(filepath.Join(dir, "nope"), opencodePlugin) {
		t.Error("missing file should be a no-op")
	}
}

// TestUninstallRemovesPluginsAndInstructions exercises the full Uninstall over a
// hermetic HOME so the home-based agents (claude/codex/gemini) and plugins
// (opencode/pi/hermes) are installed and then removed.
func TestUninstallRemovesPluginsAndInstructions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("GEMINI_HOME", filepath.Join(home, ".gemini"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
	t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// Install the home-based instruction + plugin surfaces.
	cm, _ := ClaudeMemoryPath()
	if _, err := InstallClaudeMemory(cm); err != nil {
		t.Fatal(err)
	}
	op, _ := OpenCodePluginPath()
	if _, err := InstallOpenCode(op); err != nil {
		t.Fatal(err)
	}
	pi, _ := PiPluginPath()
	if _, err := InstallPi(pi); err != nil {
		t.Fatal(err)
	}
	hd, _ := HermesPluginDir()
	if _, err := InstallHermes(hd); err != nil {
		t.Fatal(err)
	}

	workdir := t.TempDir()
	if _, err := UninstallIntegrations(workdir); err != nil {
		t.Fatalf("UninstallIntegrations: %v", err)
	}

	// Plugins are gone; the CLAUDE.md block is stripped.
	for _, p := range []string{op, pi, filepath.Join(hd, "__init__.py")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be removed after uninstall", p)
		}
	}
	if data, err := os.ReadFile(cm); err == nil {
		if containsBlock := string(data); len(containsBlock) > 0 &&
			(filepath.Base(cm) == "CLAUDE.md") && (indexOf(containsBlock, ctxWireBlockStart) >= 0) {
			t.Errorf("CLAUDE.md still contains the ctx-wire block:\n%s", containsBlock)
		}
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
