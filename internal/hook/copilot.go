package hook

import (
	"encoding/json"
	"io"

	"ctx-wire/internal/rewrite"
)

type copilotVSCodeInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type copilotCLIInput struct {
	ToolName string `json:"toolName"`
	ToolArgs string `json:"toolArgs"`
}

type copilotCLIToolArgs struct {
	Command string `json:"command"`
}

type copilotCLIOutput struct {
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

type copilotVSCodeOutput struct {
	HookSpecificOutput copilotHookOutput `json:"hookSpecificOutput"`
}

type copilotHookOutput struct {
	HookEventName            string              `json:"hookEventName"`
	PermissionDecision       string              `json:"permissionDecision"`
	PermissionDecisionReason string              `json:"permissionDecisionReason"`
	UpdatedInput             copilotUpdatedInput `json:"updatedInput"`
}

type copilotUpdatedInput struct {
	Command string `json:"command"`
}

// Copilot handles both VS Code Copilot Chat and GitHub Copilot CLI pre-tool
// payloads. VS Code can rewrite transparently. Copilot CLI cannot update input
// today, so it receives a deny-with-suggestion response.
func Copilot(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	if _, ok := raw["tool_name"]; ok {
		return copilotVSCode(data, w)
	}
	if _, ok := raw["toolName"]; ok {
		return copilotCLI(data, w)
	}
	return nil
}

func copilotVSCode(data []byte, w io.Writer) error {
	var in copilotVSCodeInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil
	}
	if in.ToolName != "runTerminalCommand" && in.ToolName != "Bash" && in.ToolName != "bash" {
		return nil
	}
	if in.ToolInput.Command == "" {
		return nil
	}
	rewritten := rewrite.Line(in.ToolInput.Command)
	if rewritten == in.ToolInput.Command {
		return nil
	}
	return json.NewEncoder(w).Encode(copilotVSCodeOutput{
		HookSpecificOutput: copilotHookOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "allow",
			PermissionDecisionReason: "ctx-wire rewrite",
			UpdatedInput:             copilotUpdatedInput{Command: rewritten},
		},
	})
}

func copilotCLI(data []byte, w io.Writer) error {
	var in copilotCLIInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil
	}
	if in.ToolName != "bash" || in.ToolArgs == "" {
		return nil
	}
	var args copilotCLIToolArgs
	if err := json.Unmarshal([]byte(in.ToolArgs), &args); err != nil {
		return nil
	}
	if args.Command == "" {
		return nil
	}
	rewritten := rewrite.Line(args.Command)
	if rewritten == args.Command {
		return nil
	}
	return json.NewEncoder(w).Encode(copilotCLIOutput{
		PermissionDecision:       "deny",
		PermissionDecisionReason: "Token savings: use `" + rewritten + "` instead",
	})
}
