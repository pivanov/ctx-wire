// Package hook implements the agent-side hook adapters that read a pre-tool-use
// payload, rewrite the shell command through ctx-wire, and emit the agent's
// expected rewrite response. Adapters fail open: on any malformed input or
// error they emit nothing, so the original command runs unchanged and the hook
// never blocks the agent.
package hook

import (
	"encoding/json"
	"io"

	"ctx-wire/internal/permission"
	"ctx-wire/internal/rewrite"
)

type claudeInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type claudeOutput struct {
	HookSpecificOutput claudeHookOutput `json:"hookSpecificOutput"`
}

type claudeHookOutput struct {
	HookEventName      string             `json:"hookEventName"`
	PermissionDecision string             `json:"permissionDecision"`
	UpdatedInput       claudeUpdatedInput `json:"updatedInput"`
}

type claudeUpdatedInput struct {
	Command string `json:"command"`
}

// Claude handles a Claude Code PreToolUse payload. If the Bash command is
// rewritable, it writes an allow decision with the updatedInput rewrite JSON to
// w; otherwise it writes nothing (no-op passthrough). It never returns a
// blocking error.
func Claude(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil // fail open
	}
	var in claudeInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil
	}
	if in.ToolName != "Bash" || in.ToolInput.Command == "" {
		return nil
	}
	rewritten := rewrite.Line(in.ToolInput.Command)
	if rewritten == in.ToolInput.Command {
		return nil // nothing to change
	}
	// Respect the user's Bash deny/ask rules. An "allow" decision here would
	// auto-approve the rewritten (wrapped) command, bypassing rules that can no
	// longer see the inner command. If a deny/ask rule matches, step aside (emit
	// nothing) so Claude applies its own decision to the original command.
	if permission.LoadClaude().Decide(in.ToolInput.Command) != permission.Allow {
		return nil
	}
	return json.NewEncoder(w).Encode(claudeOutput{
		HookSpecificOutput: claudeHookOutput{
			HookEventName:      "PreToolUse",
			PermissionDecision: "allow",
			UpdatedInput:       claudeUpdatedInput{Command: rewritten},
		},
	})
}
