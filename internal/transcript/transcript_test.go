package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSession(t *testing.T, base string, lines ...string) {
	t.Helper()
	dir := filepath.Join(base, "projects", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeExecsPairsAndScrubs is the security test: a tool_use Bash command is
// paired with its tool_result output, and an injected secret never survives into
// Exec.Output (scrub runs at the reader boundary).
func TestClaudeExecsPairsAndScrubs(t *testing.T) {
	base := t.TempDir()
	const secret = "ghp_0123456789012345678901234567890123456"
	writeSession(t, base,
		`{"type":"assistant","timestamp":"2026-06-07T10:00:00Z","message":{"content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"gh auth status"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"Logged in as me\nToken: `+secret+`\n"}]}}`,
	)

	execs, err := (Claude{}).Execs(Options{ClaudeDirs: []string{base}})
	if err != nil {
		t.Fatal(err)
	}
	if len(execs) != 1 {
		t.Fatalf("got %d execs, want 1", len(execs))
	}
	e := execs[0]
	if e.Command != "gh auth status" {
		t.Errorf("command = %q, want %q", e.Command, "gh auth status")
	}
	if !strings.Contains(e.Output, "Logged in as me") {
		t.Errorf("benign output line missing: %q", e.Output)
	}
	if strings.Contains(e.Output, secret) {
		t.Errorf("SECRET LEAKED into Exec.Output: %q", e.Output)
	}
	if e.Agent != "claude" {
		t.Errorf("agent = %q, want claude", e.Agent)
	}
}

// TestClaudeExecsScrubsBeforeCapping is the boundary security test: a secret
// that straddles the output cap must still be fully redacted. If capping ran
// first, it would slice the token into a sub-pattern fragment scrub no longer
// matches, leaking it.
func TestClaudeExecsScrubsBeforeCapping(t *testing.T) {
	base := t.TempDir()
	const secret = "ghp_0123456789012345678901234567890123456" // ghp_ + 37 chars
	pad := strings.Repeat("a", 30) + " "                       // separator so the token is \b-delimited
	writeSession(t, base,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"gh auth status"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"`+pad+secret+`"}]}}`,
	)
	// Cap lands in the middle of the secret (31 pad + ~19 into the token).
	execs, err := (Claude{}).Execs(Options{ClaudeDirs: []string{base}, MaxOutput: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(execs) != 1 {
		t.Fatalf("got %d execs, want 1", len(execs))
	}
	if strings.Contains(execs[0].Output, "ghp_") {
		t.Errorf("a secret fragment leaked across the cap boundary: %q", execs[0].Output)
	}
}

func TestClaudeExecsCapsOutput(t *testing.T) {
	base := t.TempDir()
	big := strings.Repeat("x", 1000)
	writeSession(t, base,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"cat big"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"`+big+`"}]}}`,
	)

	execs, err := (Claude{}).Execs(Options{ClaudeDirs: []string{base}, MaxOutput: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(execs) != 1 {
		t.Fatalf("got %d execs, want 1", len(execs))
	}
	if got := len(execs[0].Output); got != 100 {
		t.Errorf("output length = %d, want 100 (capped)", got)
	}
}
