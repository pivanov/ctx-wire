package hook

import (
	"encoding/json"
	"io"
	"strings"

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

// Codex handles a Codex PreToolUse payload. If the Bash command is rewritable
// it emits permissionDecision "allow" with the rewritten updatedInput.command.
// It also handles Codex's separate PermissionRequest event for a narrow
// allowlist of safe ctx-wire-wrapped tools. It never returns a blocking error:
// malformed input is a silent passthrough so Codex's normal permission flow
// remains in charge.
func Codex(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
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
			return nil
		}
		return json.NewEncoder(w).Encode(codexOutput{
			HookSpecificOutput: codexHookOutput{
				HookEventName: "PermissionRequest",
				Decision: &codexDecisionWrapper{
					Behavior: "allow",
					Message:  "ctx-wire: allow wrapped agent-browser command",
				},
			},
		})
	}
	rewritten := rewrite.Line(in.ToolInput.Command)
	if rewritten == in.ToolInput.Command {
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

func allowCodexPermissionCommand(command string) bool {
	words := firstShellWords(command, 3)
	return len(words) == 3 && words[0] == "ctx-wire" && words[1] == "run" && words[2] == "agent-browser"
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
