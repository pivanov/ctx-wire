package install

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestUninstallConsistency verifies that for every agent in agentRegistry,
// UninstallIntegrations and UninstallAgent("<name>") produce the same set of
// Removed labels when given identical installations. This ensures that a single
// agentDescriptor.Uninstall closure drives both code paths without drift.
func TestUninstallConsistency(t *testing.T) {
	for _, a := range agentRegistry {
		a := a // capture
		t.Run(a.Name, func(t *testing.T) {
			// Each sub-test gets its own hermetic HOME so path-resolution
			// functions see consistent state.
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			t.Setenv("GEMINI_HOME", filepath.Join(home, ".gemini"))
			t.Setenv("COPILOT_HOME", filepath.Join(home, ".copilot"))
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
			t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

			// workdirA and workdirB are identical copies of the installed state.
			workdirA := t.TempDir()
			workdirB := t.TempDir()

			// Install the agent into both workdirs (and into HOME for home-based agents).
			if err := installAgentForTest(t, a.Name, home, workdirA); err != nil {
				t.Fatalf("setup install %s (dirA): %v", a.Name, err)
			}
			if err := installAgentForTest(t, a.Name, home, workdirB); err != nil {
				t.Fatalf("setup install %s (dirB): %v", a.Name, err)
			}

			// Path A: uninstall via the full sweep.
			reportAll, err := UninstallIntegrations(workdirA)
			if err != nil {
				t.Fatalf("UninstallIntegrations(%s): %v", a.Name, err)
			}

			// Reset HOME-based state for the named uninstall.
			home2 := t.TempDir()
			t.Setenv("HOME", home2)
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home2, ".claude"))
			t.Setenv("CODEX_HOME", filepath.Join(home2, ".codex"))
			t.Setenv("GEMINI_HOME", filepath.Join(home2, ".gemini"))
			t.Setenv("COPILOT_HOME", filepath.Join(home2, ".copilot"))
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home2, ".pi", "agent"))
			t.Setenv("HERMES_HOME", filepath.Join(home2, ".hermes"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home2, ".config"))

			if err := installAgentForTest(t, a.Name, home2, workdirB); err != nil {
				t.Fatalf("setup install %s (dirB home2): %v", a.Name, err)
			}

			// Path B: uninstall via the named dispatch.
			reportOne, err := UninstallAgent(workdirB, a.Name)
			if err != nil {
				t.Fatalf("UninstallAgent(%s): %v", a.Name, err)
			}

			// Some labels embed an absolute HOME path (e.g. "claude:/tmp/.../001/.claude").
			// Normalize path-bearing labels to a stable relative form so the two
			// runs (different temp dirs) can be compared structurally.
			gotAll := sorted(normalizeLabels(reportAll.Removed, home, workdirA))
			gotOne := sorted(normalizeLabels(reportOne.Removed, home2, workdirB))

			if !equalSlices(gotAll, gotOne) {
				t.Errorf("Removed mismatch for %q:\n  UninstallIntegrations: %v\n  UninstallAgent:        %v",
					a.Name, gotAll, gotOne)
			}
		})
	}
}

// installAgentForTest installs the wiring for a single named agent so the
// consistency test can run both uninstall paths against identical state.
// Home-based paths are resolved at call time so they use the current HOME/env.
func installAgentForTest(t *testing.T, name, home, workdir string) error {
	t.Helper()
	switch name {
	case "claude":
		dirs, err := ClaudeConfigDirs()
		if err != nil {
			return err
		}
		for _, dir := range dirs {
			sp := filepath.Join(dir, "settings.json")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(sp, []byte(`{}`), 0o644); err != nil {
				return err
			}
			if _, err := InstallClaude(sp); err != nil {
				return err
			}
			if _, err := InstallClaudeMemory(filepath.Join(dir, "CLAUDE.md")); err != nil {
				return err
			}
		}
	case "cursor":
		path, err := CursorHooksPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if _, err := InstallCursor(path); err != nil {
			return err
		}
	case "codex":
		hooksPath, err := CodexHooksPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
			return err
		}
		if _, err := InstallCodexHooks(hooksPath); err != nil {
			return err
		}
		agentsPath, err := CodexAgentsPath()
		if err != nil {
			return err
		}
		if _, err := InstallCodexAgents(agentsPath); err != nil {
			return err
		}
	case "gemini":
		hookPath, err := GeminiHookPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
			return err
		}
		if _, err := InstallGeminiHook(hookPath); err != nil {
			return err
		}
		settingsPath, err := GeminiSettingsPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(settingsPath, []byte(`{}`), 0o644); err != nil {
			return err
		}
		if _, err := InstallGeminiSettings(settingsPath, hookPath); err != nil {
			return err
		}
		memPath, err := GeminiMemoryPath()
		if err != nil {
			return err
		}
		if _, err := InstallGeminiMemory(memPath); err != nil {
			return err
		}
	case "cline":
		if _, err := InstallCline(ClineRulesPath(workdir)); err != nil {
			return err
		}
	case "windsurf":
		if _, err := InstallWindsurf(WindsurfRulesPath(workdir)); err != nil {
			return err
		}
	case "copilot":
		if _, err := InstallCopilot(CopilotInstructionsPath(workdir), CopilotHookPath(workdir)); err != nil {
			return err
		}
		settingsPath, err := CopilotSettingsPath()
		if err != nil {
			return err
		}
		if _, err := InstallCopilotSettings(settingsPath); err != nil {
			return err
		}
	case "kilocode":
		if _, err := InstallKilocode(KilocodeRulesPath(workdir)); err != nil {
			return err
		}
	case "antigravity":
		if _, err := InstallAntigravity(AntigravityRulesPath(workdir)); err != nil {
			return err
		}
	case "vscode":
		if _, err := InstallMCP(VSCodeMCPPath(workdir), "vscode"); err != nil {
			return err
		}
	case "visualstudio":
		path, err := VisualStudioMCPPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if _, err := InstallMCP(path, "visualstudio"); err != nil {
			return err
		}
	case "opencode":
		path, err := OpenCodePluginPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if _, err := InstallOpenCode(path); err != nil {
			return err
		}
	case "pi":
		path, err := PiPluginPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if _, err := InstallPi(path); err != nil {
			return err
		}
	case "hermes":
		dir, err := HermesPluginDir()
		if err != nil {
			return err
		}
		if _, err := InstallHermes(dir); err != nil {
			return err
		}
	default:
		t.Fatalf("installAgentForTest: unhandled agent %q (update this helper)", name)
	}
	return nil
}

// normalizeLabels replaces the temp-dir-specific home and workdir prefixes in
// labels with stable placeholders so the consistency test can compare labels
// from two runs that used different temp directories.
func normalizeLabels(labels []string, home, workdir string) []string {
	out := make([]string, len(labels))
	for i, l := range labels {
		l = strings.ReplaceAll(l, home, "<home>")
		l = strings.ReplaceAll(l, workdir, "<workdir>")
		out[i] = l
	}
	return out
}

func sorted(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
