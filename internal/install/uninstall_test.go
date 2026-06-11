package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/agent"
)

func TestUninstallIntegrationsPreservesUnrelatedConfig(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	codexDir := filepath.Join(home, ".codex")
	geminiDir := filepath.Join(home, ".gemini")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("CODEX_HOME", codexDir)
	t.Setenv("GEMINI_HOME", geminiDir)

	claudePath, err := ClaudeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, claudePath, `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"other-claude"}]}]}}`)
	if _, err := InstallClaude(claudePath); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}
	// Enable the file-tools experiment too, so the full uninstall is proven to
	// strip its Read|Grep matcher (the assertNoCtxWireAndKeeps below now guards
	// it). Without removal, a dead ctx-wire hook survives the binary removal.
	if _, err := InstallClaudeFileTools(claudePath); err != nil {
		t.Fatalf("InstallClaudeFileTools: %v", err)
	}

	cursorPath, err := CursorHooksPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cursorPath, `{"hooks":{"preToolUse":[{"command":"other-cursor","matcher":"Shell"}]}}`)
	if _, err := InstallCursor(cursorPath); err != nil {
		t.Fatalf("InstallCursor: %v", err)
	}

	codexPath, err := CodexHooksPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, codexPath, `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"other-codex"}]}]}}`)
	if _, err := InstallCodexHooks(codexPath); err != nil {
		t.Fatalf("InstallCodexHooks: %v", err)
	}

	geminiHook, err := GeminiHookPath()
	if err != nil {
		t.Fatal(err)
	}
	geminiSettings, err := GeminiSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, geminiSettings, `{"hooks":{"BeforeTool":[{"matcher":"Read","hooks":[{"type":"command","command":"other-gemini"}]}]}}`)
	if _, err := InstallGeminiHook(geminiHook); err != nil {
		t.Fatalf("InstallGeminiHook: %v", err)
	}
	if _, err := InstallGeminiSettings(geminiSettings, geminiHook); err != nil {
		t.Fatalf("InstallGeminiSettings: %v", err)
	}

	clinePath := ClineRulesPath(workdir)
	windsurfPath := WindsurfRulesPath(workdir)
	copilotInstructions := CopilotInstructionsPath(workdir)
	writeFile(t, clinePath, "Keep Cline.\n")
	writeFile(t, windsurfPath, "Keep Windsurf.\n")
	writeFile(t, copilotInstructions, "Keep Copilot.\n")
	if _, err := InstallCline(clinePath); err != nil {
		t.Fatalf("InstallCline: %v", err)
	}
	if _, err := InstallWindsurf(windsurfPath); err != nil {
		t.Fatalf("InstallWindsurf: %v", err)
	}
	if _, err := InstallCopilot(copilotInstructions, CopilotHookPath(workdir)); err != nil {
		t.Fatalf("InstallCopilot: %v", err)
	}

	vscodeMCP := VSCodeMCPPath(workdir)
	visualStudioMCP, err := VisualStudioMCPPath()
	if err != nil {
		t.Fatal(err)
	}
	existingMCP := `{"servers":{"github":{"url":"https://example.invalid/mcp"}}}`
	writeFile(t, vscodeMCP, existingMCP)
	writeFile(t, visualStudioMCP, existingMCP)
	if _, err := InstallMCP(vscodeMCP, "vscode"); err != nil {
		t.Fatalf("InstallMCP vscode: %v", err)
	}
	if _, err := InstallMCP(visualStudioMCP, "visualstudio"); err != nil {
		t.Fatalf("InstallMCP visualstudio: %v", err)
	}

	report, err := UninstallIntegrations(workdir)
	if err != nil {
		t.Fatalf("UninstallIntegrations: %v", err)
	}
	if len(report.Removed) == 0 {
		t.Fatal("expected removed integrations")
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("unexpected skipped integrations: %#v", report.Skipped)
	}

	assertNoCtxWireAndKeeps(t, claudePath, "other-claude")
	assertNoCtxWireAndKeeps(t, cursorPath, "other-cursor")
	assertNoCtxWireAndKeeps(t, codexPath, "other-codex")
	assertNoCtxWireAndKeeps(t, geminiSettings, "other-gemini")
	assertMissing(t, geminiHook)
	assertNoCtxWireAndKeeps(t, clinePath, "Keep Cline.")
	assertNoCtxWireAndKeeps(t, windsurfPath, "Keep Windsurf.")
	assertNoCtxWireAndKeeps(t, copilotInstructions, "Keep Copilot.")
	assertMissing(t, CopilotHookPath(workdir))
	assertNoCtxWireAndKeeps(t, vscodeMCP, "github")
	assertNoCtxWireAndKeeps(t, visualStudioMCP, "github")
}

func TestUninstallGeminiHookSkipsCustomWrapper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ctx-wire-hook-gemini.sh")
	writeFile(t, path, "#!/bin/sh\necho custom\n")

	removed, skipped, err := UninstallGeminiHook(path)
	if err != nil {
		t.Fatalf("UninstallGeminiHook: %v", err)
	}
	if removed {
		t.Fatal("custom wrapper should not be removed")
	}
	if !skipped {
		t.Fatal("custom wrapper should be reported as skipped")
	}
	assertNoCtxWireAndKeeps(t, path, "echo custom")
}

func TestUninstallClaudeRemovesOnlyCtxWireInnerHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{
	  "hooks": {
	    "PreToolUse": [
	      {
	        "matcher": "Bash",
	        "hooks": [
	          {"type":"command","command":"ctx-wire hook claude"},
	          {"type":"command","command":"other-bash"}
	        ]
	      }
	    ]
	  }
	}`)

	changed, err := UninstallClaude(path)
	if err != nil {
		t.Fatalf("UninstallClaude: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	assertNoCtxWireAndKeeps(t, path, "other-bash")
}

// TestUninstallAgentCoversKnownAgents guards against a known agent silently
// missing a case in UninstallAgent: a missing case surfaces as the "unknown
// agent" error. No files are installed, so each call is a no-op, but the
// dispatch must still recognize every name in agent.Known.
func TestUninstallAgentCoversKnownAgents(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range agent.Known {
		if _, err := UninstallAgent(workdir, name); err != nil {
			t.Errorf("UninstallAgent(%q): %v (missing case in the switch?)", name, err)
		}
	}
	if _, err := UninstallAgent(workdir, "definitely-not-an-agent"); err == nil {
		t.Error("UninstallAgent on an unrecognized name should error")
	}
}

// TestUninstallAgentRemovesOnlyTheNamedAgent proves the scoping: uninstalling
// one agent strips its ctx-wire hook while another agent's wiring is left intact.
func TestUninstallAgentRemovesOnlyTheNamedAgent(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	claudePath, err := ClaudeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, claudePath, `{"model":"opus"}`)
	if _, err := InstallClaude(claudePath); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}
	cursorPath, err := CursorHooksPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cursorPath, `{"hooks":{"preToolUse":[{"command":"other-cursor","matcher":"Shell"}]}}`)
	if _, err := InstallCursor(cursorPath); err != nil {
		t.Fatalf("InstallCursor: %v", err)
	}

	report, err := UninstallAgent(workdir, "claude")
	if err != nil {
		t.Fatalf("UninstallAgent claude: %v", err)
	}
	if len(report.Removed) == 0 {
		t.Fatal("expected claude wiring to be removed")
	}

	claudeData, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read claude: %v", err)
	}
	if strings.Contains(string(claudeData), claudeHookCommand) {
		t.Error("claude's ctx-wire hook should be removed")
	}
	cursorData, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if !strings.Contains(string(cursorData), cursorHookCommand) {
		t.Error("cursor's ctx-wire hook must survive a claude-only uninstall")
	}
}

// TestUninstallAgentRemovesClaudeFileToolsMatcher is the round-trip guard: after
// init claude + the file-tools experiment, uninstalling claude must strip BOTH
// the Bash hook and the Read|Grep file-tools matcher, leaving no ctx-wire entry
// (an orphan would become a dead hook once the binary is gone). UninstallClaude
// removes by command, so it already covers both matchers; this pins that, and
// would fail if hook removal were ever narrowed to the Bash matcher.
func TestUninstallAgentRemovesClaudeFileToolsMatcher(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	path, err := ClaudeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, `{"hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"other-edit"}]}]}}`)
	if _, err := InstallClaude(path); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}
	if _, err := InstallClaudeFileTools(path); err != nil {
		t.Fatalf("InstallClaudeFileTools: %v", err)
	}
	pre, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !strings.Contains(string(pre), claudeFileToolsMatcher) {
		t.Fatalf("setup: file-tools matcher %q not installed:\n%s", claudeFileToolsMatcher, pre)
	}

	if _, err := UninstallAgent(workdir, "claude"); err != nil {
		t.Fatalf("UninstallAgent claude: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(after), claudeFileToolsMatcher) {
		t.Errorf("file-tools Read|Grep matcher orphaned after uninstall:\n%s", after)
	}
	assertNoCtxWireAndKeeps(t, path, "other-edit")
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertNoCtxWireAndKeeps(t *testing.T, path, keep string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(data), "ctx-wire") {
		t.Fatalf("%s still contains ctx-wire:\n%s", path, data)
	}
	if !strings.Contains(string(data), keep) {
		t.Fatalf("%s lost %q:\n%s", path, keep, data)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists after uninstall (err=%v)", path, err)
	}
}
