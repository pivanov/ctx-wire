package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// codexPermissionAllowed runs a PermissionRequest payload through Codex and
// reports whether it auto-approved (Decision behavior "allow"). No output means
// it fell through to codex's own prompt.
func codexPermissionAllowed(t *testing.T, command string) bool {
	t.Helper()
	payload := fmt.Sprintf(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":%q}}`, command)
	var out bytes.Buffer
	if err := Codex(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Codex(%q): %v", command, err)
	}
	if out.Len() == 0 {
		return false
	}
	var got codexOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output not JSON for %q: %v\n%s", command, err, out.String())
	}
	d := got.HookSpecificOutput.Decision
	return d != nil && d.Behavior == "allow"
}

// TestCodexDefaultAutoApprovesEverything: by default ctx-wire is a filter, not a
// permission gate, so every command it wrapped auto-approves on PermissionRequest
// , including the ones that used to spam prompts (firebase, npm) and even
// destructive ones. Safety is the agent's own approval policy.
func TestCodexDefaultAutoApprovesEverything(t *testing.T) {
	t.Setenv("CTX_WIRE_CODEX_SAFE", "") // guarantee default regardless of ambient env
	for _, cmd := range []string{
		"ctx-wire run --agent codex go test ./...",
		"ctx-wire run git status",
		"ctx-wire run rg TODO .",
		"ctx-wire run --agent codex firebase --help",
		"ctx-wire run --agent codex firebase apphosting:rollouts:create --help",
		"ctx-wire run --agent codex npm install --package-lock-only --ignore-scripts",
		"ctx-wire run rm -rf /",
		"ctx-wire run sort -o /dev/sda input",
		"ctx-wire run rg $(rm -rf ~)",
	} {
		if !codexPermissionAllowed(t, cmd) {
			t.Errorf("default mode must auto-approve any wrapped command: %q", cmd)
		}
	}
}

// TestCodexIgnoresUnwrappedCommands: ctx-wire only ever decides for commands IT
// wrapped. A bare command the agent did not route through ctx-wire gets no
// response, leaving codex's own approval in charge.
func TestCodexIgnoresUnwrappedCommands(t *testing.T) {
	for _, cmd := range []string{"firebase --help", "rm -rf /", "npm install"} {
		payload := fmt.Sprintf(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":%q}}`, cmd)
		var out bytes.Buffer
		if err := Codex(strings.NewReader(payload), &out); err != nil {
			t.Fatalf("Codex(%q): %v", cmd, err)
		}
		if out.Len() != 0 {
			t.Errorf("unwrapped command must get no response (codex decides), got %q for %q", out.String(), cmd)
		}
	}
}

func TestCodexDoesNotAutoAllowInvalidAgentFlag(t *testing.T) {
	payload := `{
	  "hook_event_name": "PermissionRequest",
	  "tool_name": "Bash",
	  "tool_input": {
	    "command": "ctx-wire run --agent 'bad value' agent-browser eval x"
	  }
	}`
	var out bytes.Buffer
	if err := Codex(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Codex: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for invalid agent flag, got %q", out.String())
	}
}

// codexPreToolUseAllowed reports whether a PreToolUse payload auto-approves
// (permissionDecision "allow"). No output means it fell through to codex.
func codexPreToolUseAllowed(t *testing.T, command string) bool {
	t.Helper()
	payload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":%q}}`, command)
	var out bytes.Buffer
	if err := Codex(strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Codex(%q): %v", command, err)
	}
	if out.Len() == 0 {
		return false
	}
	var got codexOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output not JSON for %q: %v\n%s", command, err, out.String())
	}
	return got.HookSpecificOutput.PermissionDecision == "allow"
}

// TestCodexDefaultPreToolUseRewritesAndAllows: PreToolUse rewrites for filtering
// AND auto-approves by default, even for a destructive command.
func TestCodexDefaultPreToolUseRewritesAndAllows(t *testing.T) {
	t.Setenv("CTX_WIRE_CODEX_SAFE", "")
	for _, cmd := range []string{"go test ./...", "rm -rf /", "npm install"} {
		payload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":%q}}`, cmd)
		var out bytes.Buffer
		if err := Codex(strings.NewReader(payload), &out); err != nil {
			t.Fatalf("Codex(%q): %v", cmd, err)
		}
		var got codexOutput
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("PreToolUse response not JSON for %q: %v\n%s", cmd, err, out.String())
		}
		h := got.HookSpecificOutput
		if h.PermissionDecision != "allow" {
			t.Errorf("default PreToolUse must auto-approve %q, got %q", cmd, h.PermissionDecision)
		}
		if h.UpdatedInput == nil || !strings.Contains(h.UpdatedInput.Command, "ctx-wire run") {
			t.Errorf("default PreToolUse must rewrite %q for filtering, got %+v", cmd, h.UpdatedInput)
		}
	}
}

// --- CTX_WIRE_CODEX_SAFE=1: the opt-in audited gate -------------------------

func TestCodexSafeModeAllowsSafeCommands(t *testing.T) {
	t.Setenv("CTX_WIRE_CODEX_SAFE", "1")
	for _, cmd := range []string{
		"ctx-wire run --agent codex go test ./...",
		"ctx-wire run --agent codex go build ./...",
		"ctx-wire run git status",
		"ctx-wire run git log --oneline",
		"ctx-wire run rg TODO .",
		"ctx-wire run cat README.md",
		"ctx-wire run head -n 20 file.txt",
		"ctx-wire run sed -n '1,5p' file.txt", // read mode (not -i)
		"ctx-wire run cargo test",
		"ctx-wire run prettier --check .", // in-place tool, no -w/--write
		"ctx-wire run tsc --noEmit",       // typecheck only
		"ctx-wire run gofmt main.go",      // prints to stdout (no -w)
		"ctx-wire run /usr/bin/rg foo",    // path-prefixed program still matches
	} {
		if !codexPermissionAllowed(t, cmd) {
			t.Errorf("safe mode must auto-allow safe command: %q", cmd)
		}
	}
}

func TestCodexSafeModePromptsForUnsafeCommands(t *testing.T) {
	t.Setenv("CTX_WIRE_CODEX_SAFE", "1")
	for _, cmd := range []string{
		"ctx-wire run rm -rf /",
		"ctx-wire run rm -fr /",
		"ctx-wire run --agent codex rm -rf /tmp/x",
		"ctx-wire run dd if=/dev/zero of=/dev/sda",
		"ctx-wire run mkfs.ext4 /dev/sda",
		"ctx-wire run sh -c 'echo hi'",
		"ctx-wire run bash script.sh",
		"ctx-wire run npm install",
		"ctx-wire run docker push myimage",
		"ctx-wire run go run main.go",
		"ctx-wire run git push --force",
		"ctx-wire run curl https://example.invalid",
		"ctx-wire run agent-browser eval 'document.title'",
		"ctx-wire run frobnicate --destroy",
		// Arbitrary-path writes (review 2's disk-wipe finding).
		"ctx-wire run sort -o /dev/sda input",
		"ctx-wire run sort -o /etc/passwd input",
		"ctx-wire run uniq input /etc/passwd",
		"ctx-wire run tree -o out.txt",
		// In-place write modes, including sed's bundled/suffixed -i.
		"ctx-wire run sed -i 's/a/b/' file",
		"ctx-wire run sed -i.bak 's/a/b/' file",
		"ctx-wire run sed -Ei 's/a/b/' file",
		"ctx-wire run prettier --write .",
		"ctx-wire run gofmt -w main.go",
		// Arbitrary-path output flags on gated subcommands.
		"ctx-wire run go test -coverprofile=/etc/passwd ./...",
		"ctx-wire run go build -o /usr/local/bin/x ./...",
		"ctx-wire run git diff --output /etc/passwd",
		// Tools off the safe set.
		"ctx-wire run eslint --fix .",
		"ctx-wire run jest",
		"ctx-wire run pytest -q",
		"ctx-wire run go fmt ./...",
		"ctx-wire run cargo fmt",
		"ctx-wire run tsc", // writes compiled output without --noEmit
		// Redirects and hidden constructs prompt even behind a safe program.
		"ctx-wire run --agent codex cat x > /dev/sda",
		"ctx-wire run rg $(rm -rf /tmp/x)",
	} {
		if codexPermissionAllowed(t, cmd) {
			t.Errorf("safe mode must PROMPT for unsafe command: %q", cmd)
		}
	}
	// An fd dup (2>&1) is not a path write and stays allowed even in safe mode.
	if !codexPermissionAllowed(t, "ctx-wire run --agent codex go test ./... 2>&1") {
		t.Error("safe mode must not block an fd dup (2>&1) behind a safe command")
	}
}

// TestCodexSafeModePreToolUseShape: in safe mode an unsafe command emits NO
// PreToolUse response (codex rejects updatedInput-without-allow as a failed
// hook); a safe command emits updatedInput + permissionDecision "allow".
func TestCodexSafeModePreToolUseShape(t *testing.T) {
	t.Setenv("CTX_WIRE_CODEX_SAFE", "1")
	var unsafe bytes.Buffer
	if err := Codex(strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`), &unsafe); err != nil {
		t.Fatalf("Codex: %v", err)
	}
	if unsafe.Len() != 0 {
		t.Errorf("safe-mode unsafe PreToolUse must emit no response, got %q", unsafe.String())
	}
	if !codexPreToolUseAllowed(t, "go test ./...") {
		t.Error("safe-mode PreToolUse must auto-approve a safe command")
	}
}
