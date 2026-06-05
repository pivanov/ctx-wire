package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCodexGoldenRewrite(t *testing.T) {
	in, err := os.ReadFile("testdata/codex_input.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/codex_output.golden")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Codex(bytes.NewReader(in), &out); err != nil {
		t.Fatalf("Codex: %v", err)
	}
	if out.String() != string(want) {
		t.Errorf("golden mismatch\n got:  %q\n want: %q", out.String(), string(want))
	}
}

func TestCodexNoopForBuiltin(t *testing.T) {
	var out bytes.Buffer
	if err := Codex(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"cd /tmp"}}`), &out); err != nil {
		t.Fatalf("Codex: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected passthrough (no output) for builtin, got %q", out.String())
	}
}

func TestCodexNoopForNonBash(t *testing.T) {
	var out bytes.Buffer
	if err := Codex(strings.NewReader(`{"tool_name":"Read","tool_input":{"command":"x"}}`), &out); err != nil {
		t.Fatalf("Codex: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for non-Bash tool, got %q", out.String())
	}
}

func TestCodexFailsOpenOnGarbage(t *testing.T) {
	var out bytes.Buffer
	if err := Codex(strings.NewReader("<<not json>>"), &out); err != nil {
		t.Errorf("expected nil error (fail open), got %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output on garbage input, got %q", out.String())
	}
}

func TestCodexAllowsPermissionForWrappedAgentBrowser(t *testing.T) {
	payload := `{
	  "hook_event_name": "PermissionRequest",
	  "tool_name": "Bash",
	  "tool_input": {
	    "command": "ctx-wire run agent-browser eval 'document.title'"
	  }
	}`
	var out bytes.Buffer
	if err := Codex(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Codex: %v", err)
	}
	var got codexOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	decision := got.HookSpecificOutput.Decision
	if got.HookSpecificOutput.HookEventName != "PermissionRequest" || decision == nil || decision.Behavior != "allow" {
		t.Fatalf("unexpected permission response: %#v", got.HookSpecificOutput)
	}
}

func TestCodexDoesNotAutoAllowUnsafeWrappedCommand(t *testing.T) {
	payload := `{
	  "hook_event_name": "PermissionRequest",
	  "tool_name": "Bash",
	  "tool_input": {
	    "command": "ctx-wire run rm -rf /"
	  }
	}`
	var out bytes.Buffer
	if err := Codex(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Codex: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for unsafe permission request, got %q", out.String())
	}
}

func TestCodexDoesNotAutoAllowAgentBrowserPrefixLookalike(t *testing.T) {
	payload := `{
	  "hook_event_name": "PermissionRequest",
	  "tool_name": "Bash",
	  "tool_input": {
	    "command": "ctx-wire run agent-browser-danger eval x"
	  }
	}`
	var out bytes.Buffer
	if err := Codex(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Codex: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for lookalike permission request, got %q", out.String())
	}
}
