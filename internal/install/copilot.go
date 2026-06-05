package install

import (
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

func CopilotInstructionsPath(workdir string) string {
	return filepath.Join(workdir, ".github", "copilot-instructions.md")
}

func CopilotHookPath(workdir string) string {
	return filepath.Join(workdir, ".github", "hooks", "ctx-wire-rewrite.json")
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
