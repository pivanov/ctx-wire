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
	copilotSettings, err := CopilotSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, clinePath, "Keep Cline.\n")
	writeFile(t, windsurfPath, "Keep Windsurf.\n")
	writeFile(t, copilotInstructions, "Keep Copilot.\n")
	writeFile(t, copilotSettings, `{"theme":"dark"}`)
	if _, err := InstallCline(clinePath); err != nil {
		t.Fatalf("InstallCline: %v", err)
	}
	if _, err := InstallWindsurf(windsurfPath); err != nil {
		t.Fatalf("InstallWindsurf: %v", err)
	}
	if _, err := InstallCopilot(copilotInstructions, CopilotHookPath(workdir)); err != nil {
		t.Fatalf("InstallCopilot: %v", err)
	}
	if _, err := InstallCopilotSettings(copilotSettings); err != nil {
		t.Fatalf("InstallCopilotSettings: %v", err)
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
	assertNoCtxWireAndKeeps(t, copilotSettings, `"theme": "dark"`)
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

// TestUninstallCopilotMixedHookPreservesForeign verifies the surgical removal
// branch of UninstallCopilotHook: when the hook file contains ctx-wire's
// managed entry alongside a foreign entry (different command), only the
// ctx-wire entry is removed and the foreign entry survives byte-for-byte.
func TestUninstallCopilotMixedHookPreservesForeign(t *testing.T) {
	dir := t.TempDir()
	hookPath := CopilotHookPath(dir)

	// First install ctx-wire's managed hook.
	if _, err := InstallCopilot(CopilotInstructionsPath(dir), hookPath); err != nil {
		t.Fatalf("InstallCopilot: %v", err)
	}

	// Now inject a foreign entry into the same PreToolUse array by rewriting
	// the hook file with both entries present. We build this from the known
	// managed JSON shape so the test is not fragile to whitespace changes.
	mixedJSON := `{
  "hooks": {
    "PreToolUse": [
      {
        "type": "command",
        "command": "ctx-wire hook copilot",
        "cwd": ".",
        "timeout": 5
      },
      {
        "type": "command",
        "command": "my-other-tool",
        "cwd": ".",
        "timeout": 10
      }
    ]
  }
}
`
	writeFile(t, hookPath, mixedJSON)

	changed, err := UninstallCopilotHook(hookPath)
	if err != nil {
		t.Fatalf("UninstallCopilotHook: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true: ctx-wire entry was present and should have been removed")
	}

	// ctx-wire entry must be gone.
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook after uninstall: %v", err)
	}
	got := string(data)
	if strings.Contains(got, "ctx-wire hook copilot") {
		t.Fatalf("ctx-wire entry survived uninstall:\n%s", got)
	}

	// Foreign entry must survive.
	if !strings.Contains(got, "my-other-tool") {
		t.Fatalf("foreign entry was deleted (uninstall was not surgical):\n%s", got)
	}
}

func TestUninstallCopilotSettingsPreservesForeign(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{
	  "theme": "dark",
	  "hooks": {
	    "preToolUse": [
	      {"type":"command","bash":"ctx-wire hook copilot"},
	      {"type":"command","command":"ctx-wire hook copilot"},
	      {"type":"command","bash":"echo keep"}
	    ]
	  }
	}`)

	changed, err := UninstallCopilotSettings(path)
	if err != nil {
		t.Fatalf("UninstallCopilotSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	assertNoCtxWireAndKeeps(t, path, "echo keep")
	assertNoCtxWireAndKeeps(t, path, `"theme": "dark"`)
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

// TestUninstallIntegrationsMultiConfig verifies that a full uninstall removes
// ctx-wire hooks from ALL detected Claude config dirs, not just the primary one.
// It also proves idempotence: the second uninstall is a no-op (no error).
func TestUninstallIntegrationsMultiConfig(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	// Primary dir: ~/.claude (no projects/ yet; always included).
	primarySettings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, primarySettings, `{}`)

	// Secondary dir: ~/.claude-main (real config: has both markers).
	secondaryDir := filepath.Join(home, ".claude-main")
	if err := os.MkdirAll(filepath.Join(secondaryDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	secondarySettings := filepath.Join(secondaryDir, "settings.json")
	writeFile(t, secondarySettings, `{"model":"sonnet","hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"other-main"}]}]}}`)

	// Excluded dir: ~/.claude-mem has settings.json but no projects/: NOT a real config.
	excludedDir := filepath.Join(home, ".claude-mem")
	if err := os.MkdirAll(excludedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	excludedSettings := filepath.Join(excludedDir, "settings.json")
	writeFile(t, excludedSettings, `{}`)

	// Wire all detected dirs.
	dirs, err := ClaudeConfigDirs()
	if err != nil {
		t.Fatalf("ClaudeConfigDirs: %v", err)
	}
	if len(dirs) < 2 {
		t.Fatalf("expected at least 2 dirs (primary + secondary), got %v", dirs)
	}
	for _, dir := range dirs {
		sp := filepath.Join(dir, "settings.json")
		if _, err := InstallClaude(sp); err != nil {
			t.Fatalf("InstallClaude %s: %v", sp, err)
		}
	}

	// Sanity: hook is present in both real dirs.
	if !hookPresent(t, primarySettings) {
		t.Error("setup: hook not present in primary settings")
	}
	if !hookPresent(t, secondarySettings) {
		t.Error("setup: hook not present in secondary settings")
	}

	// Full uninstall.
	report, err := UninstallIntegrations(workdir)
	if err != nil {
		t.Fatalf("UninstallIntegrations: %v", err)
	}
	if len(report.Removed) == 0 {
		t.Fatal("expected at least one removed entry")
	}

	// Both real dirs must be clean.
	// primarySettings may be absent (empty config file gets deleted).
	if data, rerr := os.ReadFile(primarySettings); rerr == nil {
		if strings.Contains(string(data), "ctx-wire") {
			t.Errorf("primary settings still contains ctx-wire:\n%s", data)
		}
	}
	if data, rerr := os.ReadFile(secondarySettings); rerr == nil {
		if strings.Contains(string(data), "ctx-wire") {
			t.Errorf("secondary settings still contains ctx-wire:\n%s", data)
		}
		if !strings.Contains(string(data), "other-main") {
			t.Error("secondary settings lost unrelated content")
		}
	}

	// Excluded dir must not have been touched by ctx-wire (we never installed there).
	if hookPresent(t, excludedSettings) {
		t.Error("excluded dir settings should not have been wired")
	}

	// Idempotent: second uninstall is a no-op, not an error.
	report2, err := UninstallIntegrations(workdir)
	if err != nil {
		t.Fatalf("second UninstallIntegrations: %v", err)
	}
	_ = report2
}

// TestUninstallAgentClaudeMultiConfig verifies that `ctx-wire uninstall claude`
// removes hooks from EVERY detected config dir.
func TestUninstallAgentClaudeMultiConfig(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	// Two real config dirs.
	primarySettings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, primarySettings, `{}`)
	secondaryDir := filepath.Join(home, ".claude-ship")
	if err := os.MkdirAll(filepath.Join(secondaryDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	secondarySettings := filepath.Join(secondaryDir, "settings.json")
	writeFile(t, secondarySettings, `{}`)

	dirs, err := ClaudeConfigDirs()
	if err != nil {
		t.Fatalf("ClaudeConfigDirs: %v", err)
	}
	for _, dir := range dirs {
		sp := filepath.Join(dir, "settings.json")
		if _, err := InstallClaude(sp); err != nil {
			t.Fatalf("InstallClaude %s: %v", sp, err)
		}
	}

	report, err := UninstallAgent(workdir, "claude")
	if err != nil {
		t.Fatalf("UninstallAgent claude: %v", err)
	}
	if len(report.Removed) == 0 {
		t.Fatal("expected at least one removed entry from multi-config uninstall")
	}

	// Both dirs must be clean.
	for _, sp := range []string{primarySettings, secondarySettings} {
		data, rerr := os.ReadFile(sp)
		if rerr != nil {
			continue // empty file was removed: fine
		}
		if strings.Contains(string(data), "ctx-wire") {
			t.Errorf("%s still contains ctx-wire after uninstall:\n%s", sp, data)
		}
	}
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
