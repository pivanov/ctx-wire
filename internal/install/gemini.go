package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
)

const geminiHookScript = "#!/bin/sh\nexec ctx-wire hook gemini\n"

// GeminiDir returns the Gemini CLI config directory.
func GeminiDir() (string, error) {
	if d := os.Getenv("GEMINI_HOME"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini"), nil
}

func GeminiHookPath() (string, error) {
	dir, err := GeminiDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks", "ctx-wire-hook-gemini.sh"), nil
}

// GeminiMemoryPath returns Gemini CLI's global memory file (GEMINI.md). The hook
// auto-rewrites Bash, but only an instruction file can steer the agent away
// from the built-in file tools.
func GeminiMemoryPath() (string, error) {
	dir, err := GeminiDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "GEMINI.md"), nil
}

// InstallGeminiMemory upserts the ctx-wire instruction block (incl. the
// Read/Grep steering) into Gemini's GEMINI.md. Idempotent; preserves the rest.
func InstallGeminiMemory(path string) (bool, error) {
	return upsertInstructionBlock(path, ctxWireRulesBlock)
}

func GeminiSettingsPath() (string, error) {
	dir, err := GeminiDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// InstallGeminiHook writes the thin Gemini hook wrapper and makes it executable.
func InstallGeminiHook(path string) (changed bool, err error) {
	data := []byte(geminiHookScript)
	if current, err := os.ReadFile(path); err == nil && string(current) == geminiHookScript {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return false, statErr
		}
		if info.Mode().Perm() == 0o755 {
			return false, nil
		}
		return true, os.Chmod(path, 0o755)
	}
	if err := writeAtomic(path, data, false); err != nil {
		return false, err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return false, err
	}
	return true, nil
}

// InstallGeminiSettings merges a BeforeTool/run_shell_command hook into
// settings.json.
func InstallGeminiSettings(path, hookPath string) (changed bool, err error) {
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

	hooks, ok := root["hooks"].(map[string]any)
	if root["hooks"] != nil && !ok {
		return false, fmt.Errorf("parse %s: hooks is not an object", path)
	}
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	before, ok := hooks["BeforeTool"].([]any)
	if hooks["BeforeTool"] != nil && !ok {
		return false, fmt.Errorf("parse %s: hooks.BeforeTool is not an array", path)
	}
	entry := geminiHookEntry(hookPath)
	if containsHookEntry(before, entry) {
		return false, nil
	}
	before = append(before, entry)
	hooks["BeforeTool"] = before

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeAtomic(path, append(out, '\n'), len(data) > 0); err != nil {
		return false, err
	}
	return true, nil
}

func geminiHookEntry(hookPath string) map[string]any {
	return map[string]any{
		"matcher": "run_shell_command",
		"hooks": []any{
			map[string]any{"type": "command", "command": hookPath},
		},
	}
}

func containsHookEntry(entries []any, want map[string]any) bool {
	for _, entry := range entries {
		if reflect.DeepEqual(entry, want) {
			return true
		}
	}
	return false
}
