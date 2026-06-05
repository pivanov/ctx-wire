package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// IntegrationUninstallReport describes ctx-wire integration cleanup. Removed
// contains integration labels changed or deleted by ctx-wire. Skipped contains
// ctx-wire-owned paths that were left alone because their contents no longer
// match the managed form.
type IntegrationUninstallReport struct {
	Removed []string
	Skipped []string
}

// UninstallIntegrations removes only ctx-wire-owned hook/config entries from
// known agent integrations. It preserves unrelated hooks, MCP servers, and
// rule-file content.
func UninstallIntegrations(workdir string) (IntegrationUninstallReport, error) {
	var report IntegrationUninstallReport

	if path, err := ClaudeSettingsPath(); err == nil {
		if changed, err := UninstallClaude(path); err != nil {
			return report, err
		} else if changed {
			report.Removed = append(report.Removed, "claude")
		}
	}
	if path, err := CursorHooksPath(); err == nil {
		if changed, err := UninstallCursor(path); err != nil {
			return report, err
		} else if changed {
			report.Removed = append(report.Removed, "cursor")
		}
	}
	if path, err := CodexHooksPath(); err == nil {
		if changed, err := UninstallCodexHooks(path); err != nil {
			return report, err
		} else if changed {
			report.Removed = append(report.Removed, "codex")
		}
	}
	if hookPath, err := GeminiHookPath(); err == nil {
		if settingsPath, err := GeminiSettingsPath(); err == nil {
			if changed, err := UninstallGeminiSettings(settingsPath, hookPath); err != nil {
				return report, err
			} else if changed {
				report.Removed = append(report.Removed, "gemini settings")
			}
		}
		removed, skipped, err := UninstallGeminiHook(hookPath)
		if err != nil {
			return report, err
		}
		if removed {
			report.Removed = append(report.Removed, "gemini hook")
		}
		if skipped {
			report.Skipped = append(report.Skipped, hookPath)
		}
	}

	ruleTargets := []struct {
		label string
		path  string
	}{
		{"cline rules", ClineRulesPath(workdir)},
		{"windsurf rules", WindsurfRulesPath(workdir)},
		{"copilot instructions", CopilotInstructionsPath(workdir)},
		{"kilocode rules", KilocodeRulesPath(workdir)},
		{"antigravity rules", AntigravityRulesPath(workdir)},
	}
	// Home-based memory files (claude/codex/gemini) carry the instruction block
	// too; add them best-effort.
	for _, get := range []struct {
		label string
		fn    func() (string, error)
	}{
		{"claude instructions", ClaudeMemoryPath},
		{"codex instructions", CodexAgentsPath},
		{"gemini instructions", GeminiMemoryPath},
	} {
		if p, err := get.fn(); err == nil {
			ruleTargets = append(ruleTargets, struct {
				label string
				path  string
			}{get.label, p})
		}
	}
	for _, target := range ruleTargets {
		changed, err := removeInstructionBlock(target.path)
		if err != nil {
			return report, err
		}
		if changed {
			report.Removed = append(report.Removed, target.label)
		}
	}

	changed, err := UninstallCopilotHook(CopilotHookPath(workdir))
	if err != nil {
		return report, err
	}
	if changed {
		report.Removed = append(report.Removed, "copilot hook")
	}

	mcpTargets := []struct {
		label string
		path  string
	}{
		{"vscode mcp", VSCodeMCPPath(workdir)},
	}
	if path, err := VisualStudioMCPPath(); err == nil {
		mcpTargets = append(mcpTargets, struct {
			label string
			path  string
		}{"visualstudio mcp", path})
	}
	for _, target := range mcpTargets {
		changed, err := UninstallMCP(target.path)
		if err != nil {
			return report, err
		}
		if changed {
			report.Removed = append(report.Removed, target.label)
		}
	}

	// Managed plugin files: remove only when their content still matches what
	// ctx-wire wrote, so a user-edited file at the same path is never deleted.
	pluginFiles := []struct {
		label   string
		get     func() (string, error)
		content string
	}{
		{"opencode plugin", OpenCodePluginPath, opencodePlugin},
		{"pi extension", PiPluginPath, piPlugin},
	}
	for _, p := range pluginFiles {
		path, err := p.get()
		if err != nil {
			continue
		}
		if removeFileIfContent(path, p.content) {
			report.Removed = append(report.Removed, p.label)
		}
	}
	if dir, err := HermesPluginDir(); err == nil {
		if removeFileIfContent(filepath.Join(dir, "__init__.py"), hermesPluginInit) {
			_ = os.RemoveAll(dir)
			report.Removed = append(report.Removed, "hermes plugin")
		}
	}

	return report, nil
}

// removeFileIfContent removes path only when its content equals want (so a
// user-owned file at the same path is left intact). Reports whether it removed.
func removeFileIfContent(path, want string) bool {
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		return false
	}
	return os.Remove(path) == nil
}

// UninstallClaude removes ctx-wire's Claude PreToolUse hook and preserves
// unrelated Claude settings/hooks.
func UninstallClaude(path string) (bool, error) {
	return removeNestedCommandHook(path, "hooks", "PreToolUse", claudeHookCommand)
}

// UninstallCursor removes ctx-wire's Cursor preToolUse hook and preserves
// unrelated Cursor hooks.
func UninstallCursor(path string) (bool, error) {
	return removeFlatCommandHook(path, "hooks", "preToolUse", cursorHookCommand)
}

// UninstallCodexHooks removes ctx-wire's Codex PreToolUse and
// PermissionRequest hooks, preserving unrelated hooks.
func UninstallCodexHooks(path string) (bool, error) {
	a, err := removeNestedCommandHook(path, "hooks", "PreToolUse", codexHookCommand)
	if err != nil {
		return false, err
	}
	b, err := removeNestedCommandHook(path, "hooks", "PermissionRequest", codexHookCommand)
	if err != nil {
		return false, err
	}
	return a || b, nil
}

// UninstallGeminiSettings removes ctx-wire's Gemini BeforeTool entry.
func UninstallGeminiSettings(path, hookPath string) (bool, error) {
	return removeNestedCommandHook(path, "hooks", "BeforeTool", hookPath)
}

// UninstallGeminiHook removes the managed Gemini hook wrapper. If the file
// exists but its content differs from ctx-wire's wrapper, it is skipped.
func UninstallGeminiHook(path string) (removed, skipped bool, err error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		return false, false, nil
	default:
		return false, false, err
	}
	if string(data) != geminiHookScript {
		return false, true, nil
	}
	if err := os.Remove(path); err != nil {
		return false, false, err
	}
	return true, false, nil
}

// UninstallCopilotHook removes the ctx-wire Copilot hook file when it is fully
// managed, or removes only ctx-wire hook entries from a mixed hook file.
func UninstallCopilotHook(path string) (bool, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
	if string(data) == copilotHookJSON {
		if err := os.Remove(path); err != nil {
			return false, err
		}
		return true, nil
	}
	return removeFlatCommandHook(path, "hooks", "PreToolUse", "ctx-wire hook copilot")
}

// UninstallMCP removes only the "ctx-wire" MCP server entry. If the file only
// contained that server, the file is removed.
func UninstallMCP(path string) (bool, error) {
	root, data, err := readObjectFile(path)
	if err != nil || root == nil {
		return false, err
	}
	servers, err := optionalObject(root, "servers", path)
	if err != nil || servers == nil {
		return false, err
	}
	existing, ok := servers[mcpServerName]
	if !ok {
		return false, nil
	}
	// Only remove entries that are ctx-wire's managed MCP server. A custom
	// server under the same name is user data.
	if !reflect.DeepEqual(existing, desiredMCPServer()) {
		return false, nil
	}
	delete(servers, mcpServerName)
	if len(servers) == 0 {
		delete(root, "servers")
	}
	return writeObjectOrRemove(path, root, data)
}

func removeFlatCommandHook(path, objectKey, eventKey, command string) (bool, error) {
	root, data, err := readObjectFile(path)
	if err != nil || root == nil {
		return false, err
	}
	parent, err := optionalObject(root, objectKey, path)
	if err != nil || parent == nil {
		return false, err
	}
	list, err := optionalJSONArray(parent, eventKey, path)
	if err != nil || list == nil {
		return false, err
	}
	next, changed := removeEntriesByCommand(list, command)
	if !changed {
		return false, nil
	}
	if len(next) == 0 {
		delete(parent, eventKey)
	} else {
		parent[eventKey] = next
	}
	if len(parent) == 0 {
		delete(root, objectKey)
	}
	return writeObjectOrRemove(path, root, data)
}

func removeNestedCommandHook(path, objectKey, eventKey, command string) (bool, error) {
	root, data, err := readObjectFile(path)
	if err != nil || root == nil {
		return false, err
	}
	parent, err := optionalObject(root, objectKey, path)
	if err != nil || parent == nil {
		return false, err
	}
	list, err := optionalJSONArray(parent, eventKey, path)
	if err != nil || list == nil {
		return false, err
	}

	next := make([]any, 0, len(list))
	changed := false
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			next = append(next, entry)
			continue
		}
		hooks, err := optionalJSONArray(m, "hooks", path)
		if err != nil {
			return false, err
		}
		if hooks == nil {
			next = append(next, entry)
			continue
		}
		kept, removed := removeEntriesByCommand(hooks, command)
		if !removed {
			next = append(next, entry)
			continue
		}
		changed = true
		if len(kept) == 0 {
			continue
		}
		m["hooks"] = kept
		next = append(next, m)
	}
	if !changed {
		return false, nil
	}
	if len(next) == 0 {
		delete(parent, eventKey)
	} else {
		parent[eventKey] = next
	}
	if len(parent) == 0 {
		delete(root, objectKey)
	}
	return writeObjectOrRemove(path, root, data)
}

func removeEntriesByCommand(list []any, command string) ([]any, bool) {
	next := make([]any, 0, len(list))
	changed := false
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if ok {
			if cmd, _ := m["command"].(string); cmd == command {
				changed = true
				continue
			}
		}
		next = append(next, entry)
	}
	return next, changed
}

func removeInstructionBlock(path string) (bool, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
	content := string(data)
	start := strings.Index(content, ctxWireBlockStart)
	end := strings.Index(content, ctxWireBlockEnd)
	switch {
	case start < 0 && end < 0:
		return false, nil
	case start < 0 || end < 0 || end < start:
		return false, errors.New("existing ctx-wire instruction block is malformed")
	}
	end += len(ctxWireBlockEnd)
	before := strings.TrimRight(content[:start], "\n")
	after := strings.TrimLeft(content[end:], "\n")
	var next string
	switch {
	case before == "" && strings.TrimSpace(after) == "":
		return true, os.Remove(path)
	case before == "":
		next = strings.TrimLeft(after, "\n")
	case strings.TrimSpace(after) == "":
		next = before + "\n"
	default:
		next = before + "\n\n" + after
	}
	return true, writeAtomic(path, []byte(next), true)
}

func readObjectFile(path string) (map[string]any, []byte, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, nil
	default:
		return nil, nil, err
	}
	root := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, data, nil
}

func optionalObject(parent map[string]any, key, path string) (map[string]any, error) {
	v, ok := parent[key]
	if !ok {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parse %s: %q must be an object", path, key)
	}
	return m, nil
}

func writeObjectOrRemove(path string, root map[string]any, previous []byte) (bool, error) {
	if len(root) == 0 {
		if len(previous) > 0 {
			_ = os.WriteFile(path+".bak", previous, 0o600)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		return true, nil
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	return true, writeAtomic(path, append(out, '\n'), len(previous) > 0)
}
