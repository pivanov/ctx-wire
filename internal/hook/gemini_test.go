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
	want := "{\"decision\":\"allow\",\"hookSpecificOutput\":{\"tool_input\":{\"command\":\"ctx-wire run git status\"}}}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestGeminiAllowForPassthrough(t *testing.T) {
	var out bytes.Buffer
	in := `{"tool_name":"run_shell_command","tool_input":{"command":"cd /tmp"}}`
	if err := Gemini(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Gemini: %v", err)
	}
	if got := out.String(); got != "{\"decision\":\"allow\"}\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestGeminiFailsOpenWithAllowOnGarbage(t *testing.T) {
	var out bytes.Buffer
	if err := Gemini(strings.NewReader("not-json"), &out); err != nil {
		t.Fatalf("Gemini: %v", err)
	}
	if got := out.String(); got != "{\"decision\":\"allow\"}\n" {
		t.Fatalf("output = %q", got)
	}
}
