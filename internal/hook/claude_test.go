package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// samplePayload mirrors the exact PreToolUse JSON Claude Code sends.
const samplePayload = `{
  "session_id": "abc123",
  "cwd": "/work",
  "hook_event_name": "PreToolUse",
  "tool_name": "Bash",
  "tool_input": { "command": "git status" }
}`

func TestClaudeRewritesBashCommand(t *testing.T) {
	var out bytes.Buffer
	if err := Claude(strings.NewReader(samplePayload), &out); err != nil {
		t.Fatalf("Claude: %v", err)
	}
	var got claudeOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if got.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want PreToolUse", got.HookSpecificOutput.HookEventName)
	}
	if got.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("permissionDecision = %q, want allow", got.HookSpecificOutput.PermissionDecision)
	}
	if want := "ctx-wire run git status"; got.HookSpecificOutput.UpdatedInput.Command != want {
		t.Errorf("rewritten command = %q, want %q", got.HookSpecificOutput.UpdatedInput.Command, want)
	}
}

func TestClaudeNoopForBuiltin(t *testing.T) {
	payload := `{"tool_name":"Bash","tool_input":{"command":"cd /tmp"}}`
	var out bytes.Buffer
	if err := Claude(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Claude: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output (passthrough) for builtin, got %q", out.String())
	}
}

func TestClaudeNoopForNonBash(t *testing.T) {
	payload := `{"tool_name":"Read","tool_input":{"command":"whatever"}}`
	var out bytes.Buffer
	if err := Claude(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Claude: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for non-Bash tool, got %q", out.String())
	}
}

func TestClaudeFailsOpenOnGarbage(t *testing.T) {
	var out bytes.Buffer
	if err := Claude(strings.NewReader("not json at all"), &out); err != nil {
		t.Errorf("expected nil error (fail open), got %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output on garbage input, got %q", out.String())
	}
}
