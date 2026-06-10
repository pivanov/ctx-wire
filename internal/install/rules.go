package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	ctxWireBlockStart = "<!-- ctx-wire-instructions -->"
	ctxWireBlockEnd   = "<!-- /ctx-wire-instructions -->"
)

// readGrepSteering tells the agent to route file reads and searches through the
// shell (which ctx-wire filters) instead of the built-in Read/Grep/Glob tools,
// which bypass ctx-wire entirely. Shared by every instruction surface.
const readGrepSteering = `For reading or searching files whose contents will enter your context, prefer
shell commands over the built-in tools, because shell output is filtered,
capped, and secret-scrubbed by ctx-wire while the built-in Read, Grep, and
Glob tools bypass it entirely:

- ` + "`nl -ba <file>`" + ` (numbered) or ` + "`cat`/`head`/`tail`" + ` instead of the Read tool:
  large files arrive capped instead of flooding your context.
- ` + "`rg -n <pattern> <path>`" + ` instead of the Grep tool: matches arrive grouped
  and deduplicated.
- ` + "`find`" + ` instead of the Glob tool.

Use the built-in tools only for small, exact lookups, or when you are about to
Edit a file (Edit requires a prior Read).`

const ctxWireRulesBlock = ctxWireBlockStart + `
# ctx-wire

Always prefer ctx-wire for shell commands when the command output will be read
by the AI agent.

Examples:

` + "```bash" + `
ctx-wire run git status
ctx-wire run go test ./...
ctx-wire run npm run build
ctx-wire run rg "TODO|FIXME" .
ctx-wire run docker ps
ctx-wire run kubectl get pods
` + "```" + `

Use these directly:

` + "```bash" + `
ctx-wire gain
ctx-wire explain
ctx-wire doctor
` + "```" + `

` + readGrepSteering + `

ctx-wire filters noisy command output before it enters model context. It keeps
full scrubbed logs on disk for failures and truncation.
` + ctxWireBlockEnd + `
`

func ClineRulesPath(workdir string) string {
	return filepath.Join(workdir, ".clinerules")
}

func WindsurfRulesPath(workdir string) string {
	return filepath.Join(workdir, ".windsurfrules")
}

func KilocodeRulesPath(workdir string) string {
	return filepath.Join(workdir, ".kilocode", "rules", "ctx-wire-rules.md")
}

// AntigravityRulesPath mirrors rtk's proven location (.agents/rules with an
// antigravity- prefixed file).
func AntigravityRulesPath(workdir string) string {
	return filepath.Join(workdir, ".agents", "rules", "antigravity-ctx-wire-rules.md")
}

func InstallCline(path string) (bool, error) {
	return upsertInstructionBlock(path, ctxWireRulesBlock)
}

func InstallWindsurf(path string) (bool, error) {
	return upsertInstructionBlock(path, ctxWireRulesBlock)
}

// InstallKilocode and InstallAntigravity write into nested rule directories, so
// they create the parent directory first.
func InstallKilocode(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return upsertInstructionBlock(path, ctxWireRulesBlock)
}

func InstallAntigravity(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return upsertInstructionBlock(path, ctxWireRulesBlock)
}

func upsertInstructionBlock(path, block string) (changed bool, err error) {
	data, readErr := os.ReadFile(path)
	existed := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return false, readErr
	}
	content := string(data)
	if strings.Contains(content, block) {
		return false, nil
	}

	start := strings.Index(content, ctxWireBlockStart)
	end := strings.Index(content, ctxWireBlockEnd)
	switch {
	case start >= 0 && end >= 0 && end > start:
		end += len(ctxWireBlockEnd)
		content = strings.TrimRight(content[:start], "\n") + "\n\n" + strings.TrimSpace(block) + "\n\n" + strings.TrimLeft(content[end:], "\n")
	case start >= 0 || end >= 0:
		return false, errors.New("existing ctx-wire instruction block is malformed")
	case strings.TrimSpace(content) == "":
		content = strings.TrimSpace(block) + "\n"
	default:
		content = strings.TrimRight(content, "\n") + "\n\n" + strings.TrimSpace(block) + "\n"
	}
	return true, writeAtomic(path, []byte(content), existed)
}
