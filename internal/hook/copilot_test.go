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
	want := "{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"allow\",\"permissionDecisionReason\":\"ctx-wire rewrite\",\"updatedInput\":{\"command\":\"ctx-wire run --agent copilot git status\"}}}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCopilotCLIDenyWithSuggestionByDefault(t *testing.T) {
	var out bytes.Buffer
	in := `{"toolName":"bash","toolArgs":"{\"command\":\"git status\",\"description\":\"Run requested command\"}"}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	want := "{\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"Token savings: use `ctx-wire run --agent copilot git status` instead\"}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCopilotCLITransparentRewriteStringArgsWhenEnabled(t *testing.T) {
	t.Setenv("CTX_WIRE_COPILOT_MODIFIED_ARGS", "1")
	var out bytes.Buffer
	in := `{"toolName":"bash","toolArgs":"{\"command\":\"git status\",\"description\":\"Run requested command\"}"}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	want := "{\"permissionDecision\":\"allow\",\"permissionDecisionReason\":\"ctx-wire rewrite\",\"modifiedArgs\":{\"command\":\"ctx-wire run --agent copilot git status\",\"description\":\"Run requested command\"}}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCopilotCLITransparentRewriteObjectArgsWhenEnabled(t *testing.T) {
	t.Setenv("CTX_WIRE_COPILOT_MODIFIED_ARGS", "true")
	var out bytes.Buffer
	in := `{"toolName":"bash","toolArgs":{"command":"git status","cwd":"/tmp"}}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	want := "{\"permissionDecision\":\"allow\",\"permissionDecisionReason\":\"ctx-wire rewrite\",\"modifiedArgs\":{\"command\":\"ctx-wire run --agent copilot git status\",\"cwd\":\"/tmp\"}}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCopilotCLINoopForUnsupportedArgs(t *testing.T) {
	var out bytes.Buffer
	in := `{"toolName":"bash","toolArgs":42}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
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

// Copilot CLI names its shell tool after the shell it drives, so Windows sends
// "powershell". Handling only "bash" made ctx-wire a silent no-op for every
// Windows user: the hook ran, returned no opinion, and nothing was ever wrapped.
func TestCopilotCLIPowerShellRewrite(t *testing.T) {
	var out bytes.Buffer
	in := `{"toolName":"powershell","toolArgs":"{\"command\":\"git status\"}"}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	if !strings.Contains(out.String(), "ctx-wire run --agent copilot git status") {
		t.Fatalf("powershell command was not rewritten: %q", out.String())
	}
}

// The PowerShell path must stay conservative through the hook, not just in the
// rewriter: a cmdlet is not an executable, so wrapping it would turn a working
// command into "command not found".
func TestCopilotCLIPowerShellLeavesCmdletsAlone(t *testing.T) {
	var out bytes.Buffer
	in := `{"toolName":"powershell","toolArgs":"{\"command\":\"Get-ChildItem D:\\\\src | Select-Object Name\"}"}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("cmdlet pipeline drew an opinion: %q", out.String())
	}
}

// A shell tool ctx-wire does not model gets no opinion at all, so a future
// Copilot shell cannot be silently mis-rewritten by the POSIX recognizer.
func TestCopilotCLIIgnoresUnknownShellTools(t *testing.T) {
	var out bytes.Buffer
	in := `{"toolName":"nushell","toolArgs":"{\"command\":\"git status\"}"}`
	if err := Copilot(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Copilot: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("unknown shell tool drew an opinion: %q", out.String())
	}
}
