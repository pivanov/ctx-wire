package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if _, err := InstallMCP(vscodeMCP); err != nil {
		t.Fatalf("InstallMCP vscode: %v", err)
	}
	if _, err := InstallMCP(visualStudioMCP); err != nil {
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
