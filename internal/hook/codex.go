package hook

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/rewrite"
)

type codexInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type codexOutput struct {
	HookSpecificOutput codexHookOutput `json:"hookSpecificOutput"`
}

type codexHookOutput struct {
	HookEventName      string                `json:"hookEventName"`
	PermissionDecision string                `json:"permissionDecision,omitempty"`
	UpdatedInput       *codexUpdatedInput    `json:"updatedInput,omitempty"`
	Decision           *codexDecisionWrapper `json:"decision,omitempty"`
}

type codexUpdatedInput struct {
	Command string `json:"command"`
}

type codexDecisionWrapper struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// Codex handles a Codex Bash hook payload. ctx-wire is a FILTER, not a
// permission boundary: by default it auto-approves every command it wraps so the
// agent runs uninterrupted (autonomous overnight work), and reducing/scrubbing
// output is its only job. Safety is the agent's own approval policy. On
// PreToolUse it emits permissionDecision "allow" with the rewritten command (so
// output is filtered); on PermissionRequest it approves the wrapped command. It
// never returns a blocking error: malformed input is a silent passthrough so
// Codex's normal flow stays in charge.
//
// CTX_WIRE_CODEX_SAFE=1 restores the audited safe-set gate for the cautious user:
// then only read/build/test commands that hide nothing auto-approve, and
// everything else falls through to codex's own prompt.
func Codex(r io.Reader, w io.Writer) error {
	data, err := readHookInput(r)
	if err != nil {
		return nil // fail open
	}
	var in codexInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil
	}
	if in.ToolName != "Bash" || in.ToolInput.Command == "" {
		return nil
	}
	if in.HookEventName == "PermissionRequest" {
		if !allowCodexPermissionCommand(in.ToolInput.Command) {
			return nil // not ours, or safe-mode declined → codex's own prompt
		}
		return json.NewEncoder(w).Encode(codexOutput{
			HookSpecificOutput: codexHookOutput{
				HookEventName: "PermissionRequest",
				Decision: &codexDecisionWrapper{
					Behavior: "allow",
					Message:  "ctx-wire: allow wrapped command",
				},
			},
		})
	}
	rewritten := rewrite.LineForAgent(in.ToolInput.Command, "codex")
	if rewritten == in.ToolInput.Command {
		return nil // not rewritable (already wrapped, a redirect, ...) → codex decides
	}
	// Codex REJECTS a PreToolUse response that carries updatedInput without
	// permissionDecision "allow" (it reports the hook as failed). So emit the
	// rewrite only when the command auto-approves; in safe mode an un-allowed
	// command returns nothing and codex prompts the ORIGINAL (unfiltered) command.
	if !allowCodexPermissionCommand(rewritten) {
		return nil
	}
	return json.NewEncoder(w).Encode(codexOutput{
		HookSpecificOutput: codexHookOutput{
			HookEventName:      "PreToolUse",
			PermissionDecision: "allow",
			UpdatedInput:       &codexUpdatedInput{Command: rewritten},
		},
	})
}

// allowCodexPermissionCommand reports whether a ctx-wire-wrapped command may be
// auto-approved. It first confirms the command is one ctx-wire wrapped (the
// `ctx-wire run [--agent X]` prefix); a foreign command is never touched, so
// codex's own approval stays in charge of it.
//
// By DEFAULT ctx-wire is a filter, not a gate: any command it wrapped
// auto-approves, adding zero permission friction. With CTX_WIRE_CODEX_SAFE=1 the
// audited gate is restored: the inner command must be on codexCommandIsSafe (a
// small read/build/test set), hide nothing (ContainsUnattestableConstruct), and
// redirect nowhere (ContainsRedirect); otherwise it falls through to codex.
func allowCodexPermissionCommand(command string) bool {
	words := firstShellWords(command, 8)
	if len(words) < 3 || words[0] != "ctx-wire" || words[1] != "run" {
		return false
	}
	i := 2
	if words[i] == "--agent" {
		if len(words) < 5 || agent.Normalize(words[3]) == "" {
			return false
		}
		i = 4
	} else if strings.HasPrefix(words[i], "--agent=") {
		if agent.Normalize(strings.TrimPrefix(words[i], "--agent=")) == "" {
			return false
		}
		i = 3
	}
	if i >= len(words) {
		return false
	}
	if !codexSafeOnly() {
		return true // default: filter, not a gate → auto-approve any wrapped command
	}
	return codexCommandIsSafe(words[i:]) &&
		!rewrite.ContainsUnattestableConstruct(command) &&
		!rewrite.ContainsRedirect(command)
}

// codexCommandIsSafe is the audited allowlist, built on the durable rule: a tool
// auto-approves only if it is read-only by nature (no write mode at all), or its
// write is a SINGLE in-place flag bound to its own input files that we can gate
// completely. Tools whose write mode is an arbitrary-path output flag (sort -o,
// uniq's positional output, the --output-file / --junitxml report flags) are
// deliberately ABSENT: a single missed flag would let one write to /dev/sda or
// /etc/passwd, i.e. the catastrophic class. go/cargo/git read subcommands are
// gated against go's bounded output-flag set. Everything not matched prompts.
func codexCommandIsSafe(args []string) bool {
	if len(args) == 0 {
		return false
	}
	prog := shellBase(args[0])
	rest := args[1:]
	switch {
	case codexReadOnlyPrograms[prog]:
		return true // no write mode under any argument
	case prog == "sed":
		return !sedInPlace(rest) // sed writes only via -i (any bundled/suffixed form)
	case prog == "gofmt":
		return !hasCodexArg(rest, "-w")
	case prog == "prettier":
		return !hasCodexArg(rest, "-w") && !hasCodexArg(rest, "--write")
	case prog == "tsc":
		return hasCodexArg(rest, "--noEmit") // tsc emits compiled output unless --noEmit
	default:
		if subs := codexSafeSubcommands[prog]; subs != nil {
			return len(args) >= 2 && subs[args[1]] && !hasCodexWriteFlag(args[2:])
		}
		return false
	}
}

// sedInPlace reports whether sed is in in-place (file-writing) mode. sed's only
// write flag is -i, but it shows up as -i, -i.bak (backup suffix), --in-place,
// --in-place=SUFFIX, or bundled into a short-flag group (-Ei). sed's other short
// flags (n, e, f, E, r, s, z, l, u) never contain 'i', so any short-flag arg
// carrying 'i' is in-place.
func sedInPlace(args []string) bool {
	for _, a := range args {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], 'i') {
			return true
		}
	}
	return false
}

// hasCodexWriteFlag reports whether any argument is a flag that turns a
// read/check/format tool into a file mutator (handles --flag=value too).
func hasCodexWriteFlag(args []string) bool {
	for _, a := range args {
		flag := a
		if i := strings.IndexByte(a, '='); i >= 0 {
			flag = a[:i]
		}
		if codexWriteFlags[flag] {
			return true
		}
	}
	return false
}

func hasCodexArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// codexWriteFlags is the bounded set of output flags that make a gated
// subcommand write to an arbitrary path: go test -coverprofile=/p, go build/test
// -o /p (and -c/-exec/-outputdir), cargo --out-dir/--target-dir, git diff
// --output /p. Present, they force a prompt. (sed/gofmt/prettier in-place flags
// are handled per-tool in codexCommandIsSafe.)
var codexWriteFlags = newCodexSet(
	"-o", "--output", "--output-file",
	"-c", "-coverprofile", "-outputdir", "-exec",
	"--out-dir", "--outDir", "--target-dir",
)

func shellBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// codexSafeOnly reports whether the user opted into the audited safe-set gate.
// Default is OFF: ctx-wire auto-approves every command it wraps and adds no
// permission friction, because filtering output, not policing commands, is its
// job. CTX_WIRE_CODEX_SAFE=1 restores the gate (see allowCodexPermissionCommand).
func codexSafeOnly() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("CTX_WIRE_CODEX_SAFE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// codexReadOnlyPrograms have NO write mode under any argument, so they
// auto-approve regardless of flags. Tools with a write mode are handled per-tool
// in codexCommandIsSafe (sed/gofmt/prettier/tsc), or are deliberately ABSENT and
// prompt: interpreter sinks (sh, bash, node, python), installers (npm, apt),
// network tools (curl, wget), mutating tools (rm, dd, mv, chmod), the
// arbitrary-output tools (sort -o, uniq positional, tree -o, eslint
// --output-file, the test runners' report flags), and agent-browser (eval <js>
// is a code sink). Dropped formatters/linters/test-runners can be added later
// after a per-tool write-surface audit.
var codexReadOnlyPrograms = newCodexSet(
	"rg", "grep", "egrep", "fgrep", "ag", "ack",
	"cat", "head", "tail", "nl", "ls", "file", "stat",
	"wc", "diff", "which", "basename", "dirname", "realpath", "pwd",
)

// codexSafeSubcommands gates mixed tools to their read-only subcommands; a write
// flag on top still prompts. The mutating/exec forms (go run, go fmt, go env -w,
// cargo install, cargo fmt, git push/reset/clean) are simply not listed.
var codexSafeSubcommands = map[string]map[string]bool{
	"go":    newCodexSet("test", "build", "vet", "list", "doc", "version"),
	"cargo": newCodexSet("test", "build", "check", "clippy", "tree", "doc"),
	"git":   newCodexSet("status", "log", "diff", "show", "blame", "rev-parse", "ls-files", "describe"),
}

func newCodexSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

func firstShellWords(command string, limit int) []string {
	var words []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		words = append(words, b.String())
		b.Reset()
	}
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
			if len(words) >= limit {
				return words
			}
		default:
			b.WriteRune(r)
		}
	}
	flush()
	if len(words) > limit {
		return words[:limit]
	}
	return words
}
