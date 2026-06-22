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
	SessionID     string          `json:"session_id"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	HookEventName string          `json:"hook_event_name"`
}

type claudeOutput struct {
	HookSpecificOutput claudeHookOutput `json:"hookSpecificOutput"`
}

type claudeHookOutput struct {
	HookEventName            string              `json:"hookEventName"`
	PermissionDecision       string              `json:"permissionDecision"`
	PermissionDecisionReason string              `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             *claudeUpdatedInput `json:"updatedInput,omitempty"`
}

type claudeUpdatedInput struct {
	Command string `json:"command"`
}

// Claude handles a Claude Code PreToolUse payload. For Bash: if the command is
// rewritable, it writes an allow decision with the updatedInput rewrite JSON
// to w; otherwise it writes nothing (no-op passthrough). It never returns a
// blocking error.
func Claude(r io.Reader, w io.Writer) error {
	data, err := readHookInput(r)
	if err != nil {
		return nil // fail open
	}
	var in claudeInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil
	}
	// PostToolUse fires AFTER a tool produces output. The read-ceiling spike uses
	// it to reshape native Read output (the only event that can replace, not just
	// gate, a built-in tool's result). Everything else is PreToolUse.
	if in.HookEventName == "PostToolUse" {
		return claudePostToolUse(in, w)
	}
	switch in.ToolName {
	case "Bash":
		return claudeBash(in, w)
	default:
		return nil
	}
}

func claudeBash(in claudeInput, w io.Writer) error {
	var ti struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(in.ToolInput, &ti) != nil || ti.Command == "" {
		return nil
	}
	rewritten := rewrite.LineForAgent(ti.Command, "claude")
	if rewritten == ti.Command {
		return nil // nothing to change
	}
	// Respect the user's Bash deny/ask rules. An "allow" decision here would
	// auto-approve the rewritten (wrapped) command, bypassing rules that can no
	// longer see the inner command. If a deny/ask rule matches, step aside (emit
	// nothing) so Claude applies its own decision to the original command.
	if permission.LoadClaude().Decide(ti.Command) != permission.Allow {
		return nil
	}
	return json.NewEncoder(w).Encode(claudeOutput{
		HookSpecificOutput: claudeHookOutput{
			HookEventName:      "PreToolUse",
			PermissionDecision: "allow",
			UpdatedInput:       &claudeUpdatedInput{Command: rewritten},
		},
	})
}
