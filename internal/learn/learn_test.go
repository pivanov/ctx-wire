package learn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ctx-wire/internal/discover"
	"ctx-wire/internal/ui"
)

// writeTranscript builds a Claude session JSONL under claudeDir for project and
// returns the claude config dir to scan.
func writeTranscript(t *testing.T, lines []string) (claudeDir, project string) {
	t.Helper()
	claudeDir = t.TempDir()
	project = "/work/demo-project"
	dir := filepath.Join(claudeDir, "projects", discover.EncodeClaudeProjectSlug(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return claudeDir, project
}

func TestAnalyzeDetectsCorrection(t *testing.T) {
	lines := []string{
		`{"type":"assistant","timestamp":"2026-06-01T10:00:00Z","message":{"content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"git log --one-line"}}]}}`,
		`{"type":"user","timestamp":"2026-06-01T10:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"error: unknown option '--one-line'"}]}}`,
		`{"type":"assistant","timestamp":"2026-06-01T10:00:02Z","message":{"content":[{"type":"tool_use","name":"Bash","id":"t2","input":{"command":"git log --oneline"}}]}}`,
		`{"type":"user","timestamp":"2026-06-01T10:00:03Z","message":{"content":[{"type":"tool_result","tool_use_id":"t2","is_error":false,"content":[{"type":"text","text":"abc123 first commit"}]}]}}`,
	}
	claudeDir, project := writeTranscript(t, lines)

	rep, err := Analyze(Options{ClaudeDirs: []string{claudeDir}, Project: project})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Files != 1 || rep.Sessions != 1 {
		t.Fatalf("coverage files=%d sessions=%d, want 1/1", rep.Files, rep.Sessions)
	}
	if len(rep.Rules) != 1 {
		t.Fatalf("got %d rules, want 1: %+v", len(rep.Rules), rep.Rules)
	}
	r := rep.Rules[0]
	if r.Base != "git log" || r.Right != "git log --oneline" || r.Kind != ErrUnknownFlag {
		t.Errorf("rule = %+v", r)
	}

	// The array-form tool_result content is decoded.
	out := FormatThemed(rep, ui.New(false, false, nil))
	if !strings.Contains(out, "git log") || !strings.Contains(out, "--one-line -> --oneline") {
		t.Errorf("console report missing rule:\n%s", out)
	}
}

func TestParseClaudeSessionScrubsSecrets(t *testing.T) {
	// An argv-shaped secret appears in both the command and the (re-emitted) error
	// output; ctx-wire's scrubber redacts it in each place.
	lines := []string{
		`{"type":"assistant","timestamp":"2026-06-01T10:00:00Z","message":{"content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"deploy --token supersecretvalue123"}}]}}`,
		`{"type":"user","timestamp":"2026-06-01T10:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"failed: deploy --token supersecretvalue123"}]}}`,
	}
	claudeDir, project := writeTranscript(t, lines)
	dir := filepath.Join(claudeDir, "projects", discover.EncodeClaudeProjectSlug(project))
	session := parseClaudeSession(filepath.Join(dir, "session.jsonl"), time.Time{})
	if len(session) != 1 {
		t.Fatalf("got %d execs, want 1", len(session))
	}
	if strings.Contains(session[0].Command, "supersecretvalue123") {
		t.Errorf("secret leaked in command: %q", session[0].Command)
	}
	if strings.Contains(session[0].Output, "supersecretvalue123") {
		t.Errorf("secret leaked in output: %q", session[0].Output)
	}
}

// TestRulesFileOmitsRawOutput guards the safety decision that raw error output
// (only best-effort scrubbed) never reaches the persisted, possibly-committed
// rules file.
func TestRulesFileOmitsRawOutput(t *testing.T) {
	rep := &Report{Rules: []CorrectionRule{
		{Base: "deploy", Wrong: "deploy --tokem x", Right: "deploy --token x",
			Diff: "--tokem -> --token", Kind: ErrUnknownFlag, Occurrences: 1,
			Example: "failed: some-sensitive-looking-output"},
	}}
	if strings.Contains(RulesMarkdown(rep), "some-sensitive-looking-output") {
		t.Error("rules file must not embed raw error output")
	}
}

func TestWriteRulesFile(t *testing.T) {
	root := t.TempDir()
	rep := &Report{Rules: []CorrectionRule{
		{Base: "git log", Wrong: "git log --one-line", Right: "git log --oneline", Diff: "--one-line -> --oneline", Kind: ErrUnknownFlag, Occurrences: 2, Example: "error: unknown option"},
	}}
	path, err := WriteRulesFile(rep, root)
	if err != nil {
		t.Fatalf("WriteRulesFile: %v", err)
	}
	if want := filepath.Join(root, RulesFileRelPath); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# CLI corrections", "git log", "`git log --oneline`", "--one-line -> --oneline"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("rules file missing %q:\n%s", want, data)
		}
	}
}

func TestRulesMarkdownEmpty(t *testing.T) {
	md := RulesMarkdown(&Report{})
	if !strings.Contains(md, "No repeated CLI corrections detected yet.") {
		t.Errorf("empty markdown should note nothing detected:\n%s", md)
	}
}
