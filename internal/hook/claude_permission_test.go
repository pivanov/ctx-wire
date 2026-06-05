package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain isolates the hook tests from the developer's real ~/.claude by
// pointing CLAUDE_CONFIG_DIR at an empty temp dir. Tests that need rules set
// CLAUDE_CONFIG_DIR themselves via t.Setenv.
func TestMain(m *testing.M) {
	empty, err := os.MkdirTemp("", "ctxwire-hook-empty-cfg")
	if err == nil {
		os.Setenv("CLAUDE_CONFIG_DIR", empty)
		defer os.RemoveAll(empty)
	}
	os.Exit(m.Run())
}

func TestClaudeStepsAsideOnDenyRule(t *testing.T) {
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"),
		[]byte(`{"permissions":{"deny":["Bash(git push:*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	// A denied, rewritable command: ctx-wire must emit nothing so Claude's own
	// deny rule applies to the original command (not the wrapped one).
	var out bytes.Buffer
	payload := `{"tool_name":"Bash","tool_input":{"command":"git push origin main"}}`
	if err := Claude(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Claude: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("denied command must not be auto-allowed; got output: %s", out.String())
	}

	// A non-denied, rewritable command still gets the transparent allow+rewrite.
	out.Reset()
	payload2 := `{"tool_name":"Bash","tool_input":{"command":"git status"}}`
	if err := Claude(strings.NewReader(payload2), &out); err != nil {
		t.Fatalf("Claude: %v", err)
	}
	var got claudeOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("expected allow output for non-denied command, got %q (%v)", out.String(), err)
	}
	if got.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("non-denied command should be allowed, got %q", got.HookSpecificOutput.PermissionDecision)
	}
}
