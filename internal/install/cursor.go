package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// cursorHookCommand is the hook entry ctx-wire installs for Cursor.
const cursorHookCommand = "ctx-wire hook cursor"

// CursorHooksPath returns the hooks.json path for Cursor (~/.cursor/hooks.json).
func CursorHooksPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cursor", "hooks.json"), nil
}

// InstallCursor merges the ctx-wire preToolUse/Shell hook into the Cursor
// hooks.json at path. Idempotent; preserves existing settings and hooks; writes
// atomically with a .bak backup.
func InstallCursor(path string) (changed bool, err error) {
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
	if _, ok := root["version"]; !ok {
		root["version"] = 1
	}

	hooks, err := ensureJSONObject(root, "hooks", path)
	if err != nil {
		return false, err
	}
	pre, err := optionalJSONArray(hooks, "preToolUse", path)
	if err != nil {
		return false, err
	}

	if hasCursorHook(pre) {
		return false, nil
	}
	pre = append(pre, map[string]any{
		"command": cursorHookCommand,
		"matcher": "Shell",
	})
	hooks["preToolUse"] = pre

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeAtomic(path, append(out, '\n'), len(data) > 0); err != nil {
		return false, err
	}
	return true, nil
}

func hasCursorHook(pre []any) bool {
	for _, e := range pre {
		m, _ := e.(map[string]any)
		if cmd, _ := m["command"].(string); cmd == cursorHookCommand {
			return true
		}
	}
	return false
}
