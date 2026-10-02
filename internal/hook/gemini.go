package hook

import (
	"encoding/json"
	"io"

	"ctx-wire/internal/rewrite"
)

type geminiInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

type geminiOutput struct {
	Decision           string            `json:"decision,omitempty"`
	HookSpecificOutput *geminiHookOutput `json:"hookSpecificOutput,omitempty"`
}

type geminiHookOutput struct {
	ToolInput json.RawMessage `json:"tool_input"`
}

// Gemini handles a Gemini CLI BeforeTool payload. If the command is rewritable
// it returns decision "allow" with the rewritten tool_input. Otherwise it
// ABSTAINS (emits `{}` with no decision), which is Gemini's documented
// normal-flow response, so ctx-wire only auto-approves a command it actually
// rewrites, never one it merely passes through. (An empty `{}` is used rather
// than no output, since Gemini treats non-JSON/empty stdout as a default
// "allow".) A parse failure abstains, so it can never block a command.
func Gemini(r io.Reader, w io.Writer) error {
	abstain := func() error {
		return json.NewEncoder(w).Encode(geminiOutput{})
	}
	data, err := readHookInput(r)
	if err != nil {
		return abstain()
	}
	var in geminiInput
	if err := json.Unmarshal(data, &in); err != nil {
		return abstain()
	}
	var ti struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(in.ToolInput, &ti) != nil || ti.Command == "" {
		return abstain()
	}
	if in.ToolName != "run_shell_command" {
		return abstain()
	}
	rewritten := rewrite.LineForAgent(ti.Command, "gemini")
	if rewritten == ti.Command {
		return abstain() // builtin, redirect, unattestable, ...: let Gemini decide
	}
	updated := toolInputWithCommand(in.ToolInput, rewritten)
	if updated == nil {
		return abstain()
	}
	return json.NewEncoder(w).Encode(geminiOutput{
		Decision: "allow",
		HookSpecificOutput: &geminiHookOutput{
			ToolInput: updated,
		},
	})
}
