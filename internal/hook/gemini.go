package hook

import (
	"encoding/json"
	"io"

	"ctx-wire/internal/rewrite"
)

type geminiInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type geminiOutput struct {
	Decision           string            `json:"decision"`
	HookSpecificOutput *geminiHookOutput `json:"hookSpecificOutput,omitempty"`
}

type geminiHookOutput struct {
	ToolInput geminiUpdatedInput `json:"tool_input"`
}

type geminiUpdatedInput struct {
	Command string `json:"command"`
}

// Gemini handles a Gemini CLI BeforeTool payload. Gemini expects an explicit
// allow/deny decision, so passthrough and malformed payloads emit allow.
func Gemini(r io.Reader, w io.Writer) error {
	allow := func() error {
		return json.NewEncoder(w).Encode(geminiOutput{Decision: "allow"})
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return allow()
	}
	var in geminiInput
	if err := json.Unmarshal(data, &in); err != nil {
		return allow()
	}
	if in.ToolName != "run_shell_command" || in.ToolInput.Command == "" {
		return allow()
	}
	rewritten := rewrite.Line(in.ToolInput.Command)
	if rewritten == in.ToolInput.Command {
		return allow()
	}
	return json.NewEncoder(w).Encode(geminiOutput{
		Decision: "allow",
		HookSpecificOutput: &geminiHookOutput{
			ToolInput: geminiUpdatedInput{Command: rewritten},
		},
	})
}
