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

// copilotShellMatcher scopes our preToolUse hook to the shell tools, using the
// camelCase tool names Copilot documents for the camelCase event
// (bash, powershell, create, edit, view, grep, glob, web_fetch, ask_user, task).
//
// Both shells are listed because internal/hook/copilot handles both: Copilot CLI
// names its shell tool after the shell it drives, so Windows sends "powershell"
// and Unix sends "bash". Matching only bash left Windows entirely uncovered.
//
// Scoping is blast-radius containment. A preToolUse command hook that exits
// non-zero fails CLOSED and DENIES the tool call; only timeouts fail open. While
// this entry carried no matcher, one unspawnable hook denied view, glob and task
// as well, which is what a Windows user saw on 2026-08-18: every tool refused,
// none of them tools ctx-wire touches.
const copilotShellMatcher = "bash|powershell"

const copilotCLIHookEvent = "preToolUse"

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

// InstallCopilot writes the project instructions and RETIRES the repo-local hook
// file at hookPath.
//
// ctx-wire no longer installs .github/hooks/ctx-wire-rewrite.json. That file is
// committed and shared, so it published a machine-local tool as team-wide repo
// configuration: a teammate or cloud agent without ctx-wire on PATH got a
// non-zero preToolUse result, and a non-zero preToolUse result fails CLOSED and
// denies the shell tool. Copilot runs user and repository hooks together, so the
// per-user entry could not rescue them. It also duplicated the per-user hook on
// Unix, gave Windows nothing (the matcher is bash; Copilot drives PowerShell
// there), and dirtied working trees.
//
// The per-user ~/.copilot/settings.json entry is the whole integration now.
// Retiring an existing file is part of installing, not just uninstalling:
// otherwise every already-wired repo keeps the hazard forever. Removal is
// surgical, so a hook file a user has added to keeps their entries.
func InstallCopilot(instructionsPath, hookPath string) (changed bool, err error) {
	instructionsChanged, err := upsertInstructionBlock(instructionsPath, copilotInstructionsBlock)
	if err != nil {
		return false, err
	}
	hookRetired, err := UninstallCopilotHook(hookPath)
	if err != nil {
		return false, err
	}
	return instructionsChanged || hookRetired, nil
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
		migrated := false
		for _, e := range pre {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if migrateHookCommand(em, "copilot") {
				migrated = true
			}
			// An entry wired before the matcher existed fires on EVERY tool, so a
			// hook that cannot spawn denies view/glob/task too. Scope it, and
			// upgrade an earlier managed matcher to the current one.
			if cur, _ := em["command"].(string); isHookCommand(cur, "copilot") && needsMatcherUpgrade(em) {
				em["matcher"] = copilotShellMatcher
				migrated = true
			}
		}
		if !migrated {
			return false, nil
		}
		hooks[copilotCLIHookEvent] = pre
		out, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return false, err
		}
		return true, writeAtomic(path, append(out, '\n'), len(data) > 0)
	}
	hooks[copilotCLIHookEvent] = append(pre, map[string]any{
		"type":    "command",
		"matcher": copilotShellMatcher,
		"command": hookCommand("copilot"),
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
		if cmd, _ := m["command"].(string); isHookCommand(cmd, "copilot") {
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
			if cmd, _ := m["bash"].(string); isHookCommand(cmd, "copilot") {
				changed = true
				continue
			}
			if cmd, _ := m["command"].(string); isHookCommand(cmd, "copilot") {
				changed = true
				continue
			}
		}
		next = append(next, entry)
	}
	return next, changed
}

// copilotManagedMatchers are every matcher value ctx-wire itself has written,
// including the empty one (entries wired before scoping existed). Only these may
// be replaced in place.
//
// This exists so a change to the managed matcher actually REACHES people. The
// hook command had the identical bug: installers treated "ours, in any form" as
// "already configured" and never upgraded it, so a fix shipped to nobody. A
// matcher the user chose is theirs and is never overwritten, even though that
// means their entry misses coverage for newly supported tools.
var copilotManagedMatchers = map[string]bool{
	"":                true, // absent: wired before scoping existed
	"bash":            true, // scoped before PowerShell was supported
	"bash|powershell": true, // current
}

// needsMatcherUpgrade reports whether entry's matcher is one of ours and out of
// date. A custom matcher, or one already current, returns false.
func needsMatcherUpgrade(entry map[string]any) bool {
	cur, _ := entry["matcher"].(string)
	return copilotManagedMatchers[cur] && cur != copilotShellMatcher
}
