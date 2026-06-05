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

func TestCursorNoopForBuiltin(t *testing.T) {
	var out bytes.Buffer
	if err := Cursor(strings.NewReader(`{"tool_name":"Shell","tool_input":{"command":"cd /tmp"}}`), &out); err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	assertAllowNoRewrite(t, out.Bytes())
}

func TestCursorNoopForNonShell(t *testing.T) {
	var out bytes.Buffer
	if err := Cursor(strings.NewReader(`{"tool_name":"Read","tool_input":{"command":"x"}}`), &out); err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	assertAllowNoRewrite(t, out.Bytes())
}

func TestCursorFailsOpenOnGarbage(t *testing.T) {
	var out bytes.Buffer
	if err := Cursor(strings.NewReader("}{ not json"), &out); err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	// Must still emit a valid "allow" so a parse failure never blocks a command.
	assertAllowNoRewrite(t, out.Bytes())
}

// assertAllowNoRewrite checks the output is a valid {"permission":"allow"} with
// no updated_input (i.e. a passthrough that does not block).
func assertAllowNoRewrite(t *testing.T, data []byte) {
	t.Helper()
	var got cursorOutput
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, data)
	}
	if got.Permission != "allow" {
		t.Errorf("permission = %q, want allow", got.Permission)
	}
	if got.UpdatedInput != nil {
		t.Errorf("expected no updated_input, got %+v", got.UpdatedInput)
	}
}
