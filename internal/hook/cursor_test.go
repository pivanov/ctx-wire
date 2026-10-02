package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCursorGoldenRewrite(t *testing.T) {
	in, err := os.ReadFile("testdata/cursor_input.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/cursor_output.golden")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Cursor(bytes.NewReader(in), &out); err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if out.String() != string(want) {
		t.Errorf("golden mismatch\n got:  %q\n want: %q", out.String(), string(want))
	}
}

// updated_input replaces the tool input, so a rewrite must keep every original
// field (run_in_background, timeout, ...) with its JSON type, not just command.
func TestCursorRewritePreservesToolInputFields(t *testing.T) {
	var out bytes.Buffer
	in := `{"tool_name":"Shell","tool_input":{"command":"git status","run_in_background":true,"timeout":5400000,"description":"d"}}`
	if err := Cursor(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	want := "{\"permission\":\"allow\",\"updated_input\":{\"command\":\"ctx-wire run --agent cursor git status\",\"description\":\"d\",\"run_in_background\":true,\"timeout\":5400000}}\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCursorAbstainsForBuiltin(t *testing.T) {
	var out bytes.Buffer
	if err := Cursor(strings.NewReader(`{"tool_name":"Shell","tool_input":{"command":"cd /tmp"}}`), &out); err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	assertAbstain(t, out.Bytes())
}

func TestCursorAbstainsForNonShell(t *testing.T) {
	var out bytes.Buffer
	if err := Cursor(strings.NewReader(`{"tool_name":"Read","tool_input":{"command":"x"}}`), &out); err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	assertAbstain(t, out.Bytes())
}

func TestCursorFailsOpenOnGarbage(t *testing.T) {
	var out bytes.Buffer
	if err := Cursor(strings.NewReader("}{ not json"), &out); err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	// Must still emit valid JSON and not block; abstaining lets Cursor decide.
	assertAbstain(t, out.Bytes())
}

// assertAbstain checks the output is a valid `{}` with no permission and no
// updated_input: ctx-wire vouches for nothing and lets Cursor's own flow decide.
func assertAbstain(t *testing.T, data []byte) {
	t.Helper()
	var got cursorOutput
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, data)
	}
	if got.Permission != "" {
		t.Errorf("permission = %q, want empty (abstain)", got.Permission)
	}
	if got.UpdatedInput != nil {
		t.Errorf("expected no updated_input, got %+v", got.UpdatedInput)
	}
}
