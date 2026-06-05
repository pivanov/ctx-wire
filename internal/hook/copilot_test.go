package hook

import (
	"bytes"
	"strings"
	"testing"
)

func TestCopilotVSCodeRewrite(t *testing.T) {
	var out bytes.Buffer
	in := `{"tool_name":"runTerminalCommand","tool_input":{"command":"git status"}}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	want := "{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"allow\",\"permissionDecisionReason\":\"ctx-wire rewrite\",\"updatedInput\":{\"command\":\"ctx-wire run git status\"}}}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCopilotCLIDenyWithSuggestion(t *testing.T) {
	var out bytes.Buffer
	in := `{"toolName":"bash","toolArgs":"{\"command\":\"git status\"}"}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	want := "{\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"Token savings: use `ctx-wire run git status` instead\"}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCopilotNoopForUnknown(t *testing.T) {
	var out bytes.Buffer
	if err := Copilot(strings.NewReader(`{"tool_name":"Read"}`), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
	}
}
