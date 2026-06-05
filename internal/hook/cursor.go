package hook

import (
	"encoding/json"
	"io"

	"ctx-wire/internal/rewrite"
)

type cursorInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type cursorOutput struct {
	Permission   string              `json:"permission"`
	UpdatedInput *cursorUpdatedInput `json:"updated_input,omitempty"`
}

type cursorUpdatedInput struct {
	Command string `json:"command"`
}

// Cursor handles a Cursor preToolUse payload for the Shell tool. If the command
// is rewritable it returns permission "allow" with updated_input carrying the
// rewritten command; otherwise it returns a plain "allow" (no-op passthrough).
// It always emits valid JSON and never blocks: on malformed input it still
// returns "allow" so a parse failure can never deny a command.
func Cursor(r io.Reader, w io.Writer) error {
	allow := func() error {
		return json.NewEncoder(w).Encode(cursorOutput{Permission: "allow"})
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return allow()
	}
	var in cursorInput
	if err := json.Unmarshal(data, &in); err != nil {
		return allow()
	}
	if in.ToolName != "Shell" || in.ToolInput.Command == "" {
		return allow()
	}
	rewritten := rewrite.Line(in.ToolInput.Command)
	if rewritten == in.ToolInput.Command {
		return allow()
	}
	return json.NewEncoder(w).Encode(cursorOutput{
		Permission:   "allow",
		UpdatedInput: &cursorUpdatedInput{Command: rewritten},
	})
}
