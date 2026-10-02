package hook

import (
	"bytes"
	"encoding/json"
	"reflect"
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
	if want := "ctx-wire run --agent claude git status"; updatedCommand(t, got.HookSpecificOutput.UpdatedInput) != want {
		t.Errorf("rewritten command = %q, want %q", updatedCommand(t, got.HookSpecificOutput.UpdatedInput), want)
	}
}

// Claude Code REPLACES tool_input with updatedInput, so the rewrite must carry
// every original field, not just command: dropping run_in_background made every
// long background launch run in the foreground (auto-backgrounded at 120s,
// killed at 30 min). Fields must survive with their JSON types intact.
func TestClaudeRewritePreservesToolInputFields(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cases := []struct {
		name      string
		toolInput string
	}{
		{"run_in_background bool", `{"command":"orch-agent -s m -l x | tail -15","run_in_background":true}`},
		{"timeout number", `{"command":"go test ./...","timeout":5400000}`},
		{"description string", `{"command":"go build ./...","description":"Build everything"}`},
		{"dangerouslyDisableSandbox bool", `{"command":"make deploy","dangerouslyDisableSandbox":false}`},
		{"all Bash fields together", `{"command":"orch-agent -s m -l x | tail -15","run_in_background":true,"timeout":5400000,"description":"d","dangerouslyDisableSandbox":false}`},
		{"unknown future key", `{"command":"git status","future_field":{"nested":[1,2,3]},"other":null}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":` + c.toolInput + `}`
			var out bytes.Buffer
			if err := Claude(strings.NewReader(payload), &out); err != nil {
				t.Fatalf("Claude: %v", err)
			}
			var got claudeOutput
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
			}
			updated := got.HookSpecificOutput.UpdatedInput
			if updated == nil {
				t.Fatalf("expected a rewrite, got %s", out.String())
			}
			var origFields, updatedFields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(c.toolInput), &origFields); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(updated, &updatedFields); err != nil {
				t.Fatalf("updatedInput is not a JSON object: %v\n%s", err, updated)
			}
			if len(updatedFields) != len(origFields) {
				t.Errorf("updatedInput has %d keys, want %d (dropped fields?)\n%s", len(updatedFields), len(origFields), updated)
			}
			for key, want := range origFields {
				if key == "command" {
					continue
				}
				got, ok := updatedFields[key]
				if !ok {
					t.Errorf("updatedInput dropped key %q\n%s", key, updated)
					continue
				}
				if !jsonEqual(got, want) {
					t.Errorf("key %q = %s, want %s (type changed?)", key, got, want)
				}
			}
			var cmd string
			if err := json.Unmarshal(updatedFields["command"], &cmd); err != nil || !strings.Contains(cmd, "ctx-wire run --agent claude") {
				t.Errorf("command not rewritten: %q (err %v)", cmd, err)
			}
		})
	}
}

// A command ctx-wire does not rewrite must stay a silent passthrough even when
// tool_input carries extra fields: emitting nothing lets Claude run the original
// input untouched.
func TestClaudeNoRewriteKeepsSilentWithExtraFields(t *testing.T) {
	payload := `{"tool_name":"Bash","tool_input":{"command":"cd /tmp","run_in_background":true,"timeout":60000,"description":"d"}}`
	var out bytes.Buffer
	if err := Claude(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Claude: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output (passthrough) for non-rewritten command, got %q", out.String())
	}
}

// updatedCommand extracts the rewritten command from a raw updatedInput.
func updatedCommand(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var ti struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &ti); err != nil {
		t.Fatalf("updatedInput is not an object with a command: %v\n%s", err, raw)
	}
	return ti.Command
}

// jsonEqual compares two raw JSON values semantically (number formatting and
// object key order aside).
func jsonEqual(a, b json.RawMessage) bool {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
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
