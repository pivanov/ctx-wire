// Package install wires ctx-wire into agent configuration. For Claude Code it
// merges a PreToolUse/Bash hook into settings.json without disturbing existing
// settings, writing atomically with a .bak backup. It is idempotent.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// claudeHookCommand is the hook entry ctx-wire installs.
const claudeHookCommand = "ctx-wire hook claude"

// ClaudeSettingsPath returns the settings.json path for Claude Code, honoring
// CLAUDE_CONFIG_DIR and falling back to ~/.claude/settings.json.
func ClaudeSettingsPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// ClaudeMemoryPath returns Claude Code's global memory file (CLAUDE.md),
// honoring CLAUDE_CONFIG_DIR, falling back to ~/.claude/CLAUDE.md. The hook
// auto-rewrites Bash, but only an instruction file can steer the agent away
// from the built-in Read/Grep/Glob tools (which bypass ctx-wire).
func ClaudeMemoryPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "CLAUDE.md"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "CLAUDE.md"), nil
}

// InstallClaudeMemory upserts the ctx-wire instruction block (incl. the
// Read/Grep steering) into Claude's CLAUDE.md. Idempotent; preserves the rest.
func InstallClaudeMemory(path string) (bool, error) {
	return upsertInstructionBlock(path, ctxWireRulesBlock)
}

// InstallClaude merges the ctx-wire PreToolUse hook into the settings file at
// path. It returns changed=false when the hook is already present. Existing
// settings and other hooks are preserved.
func InstallClaude(path string) (changed bool, err error) {
	root := map[string]any{}
	data, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return false, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(readErr, fs.ErrNotExist):
		// new file
	default:
		return false, readErr
	}
	if root == nil {
		root = map[string]any{}
	}

	hooks, err := ensureJSONObject(root, "hooks", path)
	if err != nil {
		return false, err
	}
	pre, err := optionalJSONArray(hooks, "PreToolUse", path)
	if err != nil {
		return false, err
	}

	if hasClaudeHook(pre) {
		return false, nil
	}

	pre = append(pre, map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{"type": "command", "command": claudeHookCommand},
		},
	})
	hooks["PreToolUse"] = pre

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeAtomic(path, append(out, '\n'), len(data) > 0); err != nil {
		return false, err
	}
	return true, nil
}

func hasClaudeHook(pre []any) bool {
	for _, e := range pre {
		m, _ := e.(map[string]any)
		hs, _ := m["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); cmd == claudeHookCommand {
				return true
			}
		}
	}
	return false
}

// writeAtomic writes data to path via a temp file + rename. If backup is true
// and path exists, the previous contents are saved to path+".bak" first.
func writeAtomic(path string, data []byte, backup bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if backup {
		if prev, err := os.ReadFile(path); err == nil {
			_ = os.WriteFile(path+".bak", prev, mode)
		}
	}
	tmp, err := os.CreateTemp(dir, ".ctx-wire-settings-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
