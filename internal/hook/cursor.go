package hook

import (
	"encoding/json"
	"io"

	"ctx-wire/internal/rewrite"
)

type cursorInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

type cursorOutput struct {
	Permission   string          `json:"permission,omitempty"`
	UpdatedInput json.RawMessage `json:"updated_input,omitempty"`
}

// Cursor handles a Cursor preToolUse payload for the Shell tool. If the command
// is rewritable it returns permission "allow" with updated_input carrying the
// rewritten command. Otherwise it ABSTAINS (emits `{}` with no permission), so
// Cursor's own permission flow decides: ctx-wire only auto-approves a command it
// actually rewrites, never one it merely passes through. Cursor's `permission:
// "allow"` means "execute without asking", so emitting it on passthrough would
// override the user's own ask rules for every command. It always emits valid
// JSON and never denies: a parse failure abstains, so it can never block a
// command.
func Cursor(r io.Reader, w io.Writer) error {
	abstain := func() error {
		return json.NewEncoder(w).Encode(cursorOutput{})
	}
	data, err := readHookInput(r)
	if err != nil {
		return abstain()
	}
	var in cursorInput
	if err := json.Unmarshal(data, &in); err != nil {
		return abstain()
	}
	var ti struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(in.ToolInput, &ti) != nil || ti.Command == "" {
		return abstain()
	}
	if in.ToolName != "Shell" {
		return abstain()
	}
	rewritten := rewrite.LineForAgent(ti.Command, "cursor")
	if rewritten == ti.Command {
		return abstain() // builtin, redirect, unattestable, ...: let Cursor decide
	}
	updated := toolInputWithCommand(in.ToolInput, rewritten)
	if updated == nil {
		return abstain()
	}
	return json.NewEncoder(w).Encode(cursorOutput{
		Permission:   "allow",
		UpdatedInput: updated,
	})
}
