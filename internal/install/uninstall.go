package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
// rule-file content. Each agent's uninstall logic is defined once in
// agentRegistry (agent_registry.go) and iterated here.
func UninstallIntegrations(workdir string) (IntegrationUninstallReport, error) {
	var report IntegrationUninstallReport
	for _, a := range agentRegistry {
		if err := a.Uninstall(workdir, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// UninstallAgent removes only the named agent's ctx-wire wiring: its
// hook/plugin/MCP entry and its instruction or rules block. It reuses the same
// surgical helpers as UninstallIntegrations, scoped to one agent, so the binary,
// managed shims, ctx-wire config/data, and every OTHER agent are left intact.
// An unrecognized name is an error. Each agent's logic is defined once in
// agentRegistry (agent_registry.go), so adding an agent only requires a new
// table entry. TestUninstallAgentCoversKnownAgents enforces full coverage.
func UninstallAgent(workdir, name string) (IntegrationUninstallReport, error) {
	var report IntegrationUninstallReport
	a, ok := registryByName(name)
	if !ok {
		return report, errUnknownAgent(name)
	}
	return report, a.Uninstall(workdir, &report)
}

// removeInstr removes ctx-wire's instruction block at path and records label
// when it changed. A method on the report so the per-agent cases stay terse.
func (r *IntegrationUninstallReport) removeInstr(label, path string) error {
	changed, err := removeInstructionBlock(path)
	if err != nil {
		return err
	}
	if changed {
		r.Removed = append(r.Removed, label)
	}
	return nil
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

// UninstallClaude removes ctx-wire's Claude PreToolUse and PostToolUse hooks
// (the read-ceiling spike installs a PostToolUse entry) and preserves unrelated
// Claude settings/hooks.
func UninstallClaude(path string) (bool, error) {
	pre, err := removeNestedCommandHook(path, "hooks", "PreToolUse", claudeHookCommand)
	if err != nil {
		return false, err
	}
	post, err := removeNestedCommandHook(path, "hooks", "PostToolUse", claudeHookCommand)
	if err != nil {
		return false, err
	}
	return pre || post, nil
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
	if !isManagedMCPServer(existing) {
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
