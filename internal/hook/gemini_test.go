package hook

import (
	"bytes"
	"strings"
	"testing"
)

func TestGeminiRewrite(t *testing.T) {
	var out bytes.Buffer
	in := `{"tool_name":"run_shell_command","tool_input":{"command":"git status"}}`
	if err := Gemini(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Gemini: %v", err)
	}
	want := "{\"decision\":\"allow\",\"hookSpecificOutput\":{\"tool_input\":{\"command\":\"ctx-wire run --agent gemini git status\"}}}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

// A rewritten tool_input must keep every original field, not just command: the
// host replaces the tool input with what the hook returns.
func TestGeminiRewritePreservesToolInputFields(t *testing.T) {
	var out bytes.Buffer
	in := `{"tool_name":"run_shell_command","tool_input":{"command":"git status","timeout":5400000,"run_in_background":true}}`
	if err := Gemini(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Gemini: %v", err)
	}
	want := "{\"decision\":\"allow\",\"hookSpecificOutput\":{\"tool_input\":{\"command\":\"ctx-wire run --agent gemini git status\",\"run_in_background\":true,\"timeout\":5400000}}}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestGeminiAbstainsForPassthrough(t *testing.T) {
	var out bytes.Buffer
	in := `{"tool_name":"run_shell_command","tool_input":{"command":"cd /tmp"}}`
	if err := Gemini(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Gemini: %v", err)
	}
	// Abstain: no decision, so Gemini's own permission flow applies.
	if got := out.String(); got != "{}\n" {
		t.Fatalf("output = %q, want abstain {}", got)
	}
}

func TestGeminiFailsOpenWithAbstainOnGarbage(t *testing.T) {
	var out bytes.Buffer
	if err := Gemini(strings.NewReader("not-json"), &out); err != nil {
		t.Fatalf("Gemini: %v", err)
	}
	if got := out.String(); got != "{}\n" {
		t.Fatalf("output = %q, want abstain {}", got)
	}
}
