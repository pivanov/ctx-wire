package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const codexHookCommand = "ctx-wire hook codex"

// CodexHooksPath returns ~/.codex/hooks.json.
func CodexHooksPath() (string, error) {
	dir, err := codexDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks.json"), nil
}

// CodexAgentsPath returns ~/.codex/AGENTS.md. The hook auto-rewrites Bash, but
// only an instruction file can steer the agent away from built-in file tools.
func CodexAgentsPath() (string, error) {
	dir, err := codexDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "AGENTS.md"), nil
}

// InstallCodexAgents upserts the ctx-wire instruction block (incl. the
// Read/Grep steering) into Codex's AGENTS.md. Idempotent; preserves the rest.
func InstallCodexAgents(path string) (bool, error) {
	return upsertInstructionBlock(path, ctxWireRulesBlock)
}

// CodexConfigPath returns ~/.codex/config.toml.
func CodexConfigPath() (string, error) {
	dir, err := codexDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

func codexDir() (string, error) {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// InstallCodexHooks merges the ctx-wire Bash hooks into the Codex hooks.json at
// path. Codex uses top-level "hooks" arrays keyed by event name. ctx-wire needs
// both PreToolUse (to rewrite the command) and PermissionRequest (to answer
// Codex's separate permission gate for narrow allowlisted wrapped commands).
// Idempotent; preserves existing hooks; atomic write with .bak. This does NOT
// enable the hooks feature or grant trust: both are deliberate user steps
// surfaced by the init command.
func InstallCodexHooks(path string) (changed bool, err error) {
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
	changed, err = ensureCodexHook(hooks, "PreToolUse", path)
	if err != nil {
		return false, err
	}
	permChanged, err := ensureCodexHook(hooks, "PermissionRequest", path)
	if err != nil {
		return false, err
	}
	changed = permChanged || changed
	if !changed {
		return false, nil
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeAtomic(path, append(out, '\n'), len(data) > 0); err != nil {
		return false, err
	}
	return true, nil
}

func ensureCodexHook(hooks map[string]any, event, path string) (bool, error) {
	list, err := optionalJSONArray(hooks, event, path)
	if err != nil {
		return false, err
	}
	if hasCodexHook(list) {
		return false, nil
	}
	list = append(list, map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{"type": "command", "command": codexHookCommand},
		},
	})
	hooks[event] = list
	return true, nil
}

func hasCodexHook(pre []any) bool {
	for _, e := range pre {
		m, _ := e.(map[string]any)
		hs, _ := m["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); cmd == codexHookCommand {
				return true
			}
		}
	}
	return false
}

// CodexHooksEnabled reports whether [features] hooks = true in the config.toml
// at path. A missing file or missing key reports false. ctx-wire never flips
// this flag itself; enabling hooks is a user decision.
func CodexHooksEnabled(configPath string) (bool, error) {
	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var cfg struct {
		Features struct {
			Hooks bool `toml:"hooks"`
		} `toml:"features"`
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("parse %s: %w", configPath, err)
	}
	return cfg.Features.Hooks, nil
}
