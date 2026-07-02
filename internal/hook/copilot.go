package hook

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"ctx-wire/internal/rewrite"
)

type copilotVSCodeInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type copilotCLIInput struct {
	ToolName string          `json:"toolName"`
	ToolArgs json.RawMessage `json:"toolArgs"`
}

type copilotCLIOutput struct {
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	ModifiedArgs             any    `json:"modifiedArgs,omitempty"`
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
// payloads. VS Code supports transparent rewrites. Copilot CLI sends toolArgs
// as a JSON string in current releases, while the SDK type allows an object, so
// copilotCLI accepts both and preserves unknown fields. The CLI transparent
// rewrite response is capability-gated; older CLIs that ignore modifiedArgs
// must keep receiving deny-with-suggestion so the original command never runs raw.
func Copilot(r io.Reader, w io.Writer) error {
	data, err := readHookInput(r)
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
	rewritten := rewrite.LineForAgent(in.ToolInput.Command, "copilot")
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
	if in.ToolName != "bash" || len(in.ToolArgs) == 0 {
		return nil
	}
	args, ok := parseCopilotCLIToolArgs(in.ToolArgs)
	if !ok {
		return nil
	}
	command, _ := args["command"].(string)
	if command == "" {
		return nil
	}
	rewritten := rewrite.LineForAgent(command, "copilot")
	if rewritten == command {
		return nil
	}
	if !copilotCLIModifiedArgsEnabled() {
		return json.NewEncoder(w).Encode(copilotCLIOutput{
			PermissionDecision:       "deny",
			PermissionDecisionReason: "Token savings: use `" + rewritten + "` instead",
		})
	}
	args["command"] = rewritten
	return json.NewEncoder(w).Encode(copilotCLIOutput{
		PermissionDecision:       "allow",
		PermissionDecisionReason: "ctx-wire rewrite",
		ModifiedArgs:             args,
	})
}

func copilotCLIModifiedArgsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CTX_WIRE_COPILOT_MODIFIED_ARGS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseCopilotCLIToolArgs(raw json.RawMessage) (map[string]any, bool) {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		raw = []byte(encoded)
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, false
	}
	return args, true
}
