package install

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const copilotInstructionsBlock = ctxWireBlockStart + `
# ctx-wire

Prefer ctx-wire for shell commands that will be shown to the model.

` + "```bash" + `
ctx-wire run git status
ctx-wire run go test ./...
ctx-wire run npm run build
ctx-wire run rg "TODO|FIXME" .
` + "```" + `

` + readGrepSteering + `

` + mcpToolsSteering + `

Use ` + "`ctx-wire gain`" + ` to inspect savings and ` + "`ctx-wire explain`" + ` to find commands
that still need tuning.
` + ctxWireBlockEnd + `
`

const copilotHookJSON = `{
  "hooks": {
    "PreToolUse": [
      {
        "type": "command",
        "command": "ctx-wire hook copilot",
        "cwd": ".",
        "timeout": 5
      }
    ]
  }
}
`

const (
	copilotCLIHookCommand = "ctx-wire hook copilot"
	copilotCLIHookEvent   = "preToolUse"
)

func CopilotInstructionsPath(workdir string) string {
	return filepath.Join(workdir, ".github", "copilot-instructions.md")
}

func CopilotHookPath(workdir string) string {
	return filepath.Join(workdir, ".github", "hooks", "ctx-wire-rewrite.json")
}

func CopilotDir() (string, error) {
	if d := os.Getenv("COPILOT_HOME"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".copilot"), nil
}

func CopilotSettingsPath() (string, error) {
	dir, err := CopilotDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func InstallCopilot(instructionsPath, hookPath string) (changed bool, err error) {
	instructionsChanged, err := upsertInstructionBlock(instructionsPath, copilotInstructionsBlock)
	if err != nil {
		return false, err
	}
	hookChanged, err := writeFileIfChanged(hookPath, []byte(copilotHookJSON), 0o644)
	if err != nil {
		return false, err
	}
	return instructionsChanged || hookChanged, nil
}

func InstallCopilotSettings(path string) (bool, error) {
	root, data, err := readObjectFile(path)
	if err != nil {
		return false, err
	}
	if root == nil {
		root = map[string]any{}
	}
	hooks, err := ensureJSONObject(root, "hooks", path)
	if err != nil {
		return false, err
	}
	pre, err := optionalJSONArray(hooks, copilotCLIHookEvent, path)
	if err != nil {
		return false, err
	}
	if hasCopilotCLIHook(pre) {
		return false, nil
	}
	hooks[copilotCLIHookEvent] = append(pre, map[string]any{
		"type":    "command",
		"command": copilotCLIHookCommand,
	})
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	return true, writeAtomic(path, append(out, '\n'), len(data) > 0)
}

func hasCopilotCLIHook(pre []any) bool {
	for _, entry := range pre {
		m, _ := entry.(map[string]any)
		if cmd, _ := m["command"].(string); cmd == copilotCLIHookCommand {
			return true
		}
	}
	return false
}

func UninstallCopilotSettings(path string) (bool, error) {
	root, data, err := readObjectFile(path)
	if err != nil || root == nil {
		return false, err
	}
	hooks, err := optionalObject(root, "hooks", path)
	if err != nil || hooks == nil {
		return false, err
	}
	pre, err := optionalJSONArray(hooks, copilotCLIHookEvent, path)
	if err != nil || pre == nil {
		return false, err
	}
	next, changed := removeCopilotCLIHooks(pre)
	if !changed {
		return false, nil
	}
	if len(next) == 0 {
		delete(hooks, copilotCLIHookEvent)
	} else {
		hooks[copilotCLIHookEvent] = next
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return writeObjectOrRemove(path, root, data)
}

func removeCopilotCLIHooks(pre []any) ([]any, bool) {
	next := make([]any, 0, len(pre))
	changed := false
	for _, entry := range pre {
		m, ok := entry.(map[string]any)
		if ok {
			if cmd, _ := m["bash"].(string); cmd == copilotCLIHookCommand {
				changed = true
				continue
			}
			if cmd, _ := m["command"].(string); cmd == copilotCLIHookCommand {
				changed = true
				continue
			}
		}
		next = append(next, entry)
	}
	return next, changed
}

func writeFileIfChanged(path string, data []byte, perm os.FileMode) (bool, error) {
	if current, err := os.ReadFile(path); err == nil && string(current) == string(data) {
		return false, nil
	}
	if err := writeAtomic(path, data, true); err != nil {
		return false, err
	}
	if err := os.Chmod(path, perm); err != nil {
		return false, err
	}
	return true, nil
}
