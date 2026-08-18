package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

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
		// Present, but possibly in an older command form. Canonicalize it instead
		// of reporting "already configured" and leaving an entry the agent cannot
		// spawn (see migrateHookCommand).
		migrated := false
		for _, e := range pre {
			if em, ok := e.(map[string]any); ok && migrateHookCommand(em, "cursor") {
				migrated = true
			}
		}
		if !migrated {
			return false, nil
		}
	} else {
		pre = append(pre, map[string]any{
			"command": hookCommand("cursor"),
			"matcher": "Shell",
		})
	}
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
		if cmd, _ := m["command"].(string); isHookCommand(cmd, "cursor") {
			return true
		}
	}
	return false
}
