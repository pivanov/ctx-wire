package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeClaudeSession writes a minimal Claude .jsonl transcript with the given
// Bash commands, under base/projects/<slug>/<name>.jsonl.
func writeClaudeSessionFile(t *testing.T, base, project, name string, commands ...string) {
	t.Helper()
	dir := filepath.Join(base, "projects", encodeClaudeProjectSlug(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for _, c := range commands {
		line := `{"type":"assistant","timestamp":"2026-06-04T10:00:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":` + jsonString(c) + `}}]}}` + "\n"
		b = append(b, line...)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func jsonString(s string) string {
	out := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		default:
			out = append(out, string(r)...)
		}
	}
	return string(append(out, '"'))
}

// TestSessionsCountsFileTools proves the file-tool axis: Read/Grep tool uses
// and read-before-edit refusals are counted per session, in both tool_result
// content shapes (plain string and text-block array), without touching the
// shell-only Coverable/Covered semantics.
func TestSessionsCountsFileTools(t *testing.T) {
	base := t.TempDir()
	project := "/work/proj"
	dir := filepath.Join(base, "projects", encodeClaudeProjectSlug(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		// One coverable shell command so the session qualifies.
		`{"type":"assistant","timestamp":"2026-06-04T10:00:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]}}`,
		// Two Reads and one Grep (built-in tools, no command field).
		`{"type":"assistant","timestamp":"2026-06-04T10:01:00Z","message":{"content":[{"type":"tool_use","name":"Read","input":{}},{"type":"tool_use","name":"Read","input":{}}]}}`,
		`{"type":"assistant","timestamp":"2026-06-04T10:02:00Z","message":{"content":[{"type":"tool_use","name":"Grep","input":{}}]}}`,
		// Edit refusal as a plain-string tool_result (user line) , real errors carry is_error.
		`{"type":"user","timestamp":"2026-06-04T10:03:00Z","message":{"content":[{"type":"tool_result","is_error":true,"content":"File has not been read yet. Read it first before writing to it."}]}}`,
		// Edit refusal as a text-block array tool_result.
		`{"type":"user","timestamp":"2026-06-04T10:04:00Z","message":{"content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"Error: file has not been read yet."}]}]}}`,
		// A normal tool_result must NOT count.
		`{"type":"user","timestamp":"2026-06-04T10:05:00Z","message":{"content":[{"type":"tool_result","content":"ok"}]}}`,
		// A file-tools capture deny: a Read redirected to a filtered shell read (is_error).
		`{"type":"user","timestamp":"2026-06-04T10:06:00Z","message":{"content":[{"type":"tool_result","is_error":true,"content":"Token savings: run nl -ba /work/proj/big.go in Bash instead (the output is filtered, capped, and secrets-scrubbed by ctx-wire; the built-in tool bypasses that)."}]}}`,
		// FALSE-POSITIVE GUARDS: a successful Bash tool_result that merely ECHOES a
		// marker (cat of the source, or the deny JSON) must NOT count, only is_error
		// results are real denies/refusals.
		`{"type":"user","timestamp":"2026-06-04T10:07:00Z","message":{"content":[{"type":"tool_result","content":"const captureMarker = \"Token savings: run \" // has not been read yet"}]}}`,
	}
	var b []byte
	for _, l := range lines {
		b = append(b, l...)
		b = append(b, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "ft.jsonl"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	stats, err := Sessions(Options{ClaudeDirs: []string{base}, Project: project})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 session, got %d", len(stats))
	}
	ft := stats[0].FileTools
	if ft.Reads != 2 || ft.Greps != 1 || ft.EditRefusals != 2 || ft.Captures != 1 {
		t.Errorf("FileTools = %+v, want Reads=2 Greps=1 EditRefusals=2 Captures=1", ft)
	}
	if stats[0].Coverable != 1 || stats[0].Covered != 0 {
		t.Errorf("shell adoption semantics moved: %+v", stats[0])
	}
}

func TestSessionsAdoption(t *testing.T) {
	base := t.TempDir()
	project := "/work/proj"
	// 2 routed (ctx-wire), 1 escaped (git status, coverable), 1 builtin (cd, not
	// coverable). Adoption = 2 / 3.
	writeClaudeSessionFile(t, base, project, "s1.jsonl",
		"ctx-wire run rg TODO .",
		"ctx-wire run cat file.go",
		"git status",
		"cd /tmp",
	)

	stats, err := Sessions(Options{ClaudeDirs: []string{base}, Project: project})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 session, got %d", len(stats))
	}
	s := stats[0]
	if s.Agent != "claude" || s.File != "s1.jsonl" {
		t.Errorf("session meta wrong: %+v", s)
	}
	if s.Coverable != 3 || s.Covered != 2 {
		t.Fatalf("coverage wrong: coverable=%d covered=%d (want 3, 2)", s.Coverable, s.Covered)
	}
	if got := s.AdoptionPct(); got < 66.0 || got > 67.0 {
		t.Errorf("adoption = %.1f, want ~66.7", got)
	}
}

func TestSessionsSkipsNonRoutable(t *testing.T) {
	base := t.TempDir()
	project := "/work/proj"
	// Only a builtin and a pipeline-to-redirect (not wrappable) → session skipped.
	writeClaudeSessionFile(t, base, project, "empty.jsonl", "cd /tmp", "echo hi")
	stats, err := Sessions(Options{ClaudeDirs: []string{base}, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Errorf("session with no routable commands should be skipped, got %+v", stats)
	}
}

// TestSessionsIncludesFileToolOnlySessions pins the baseline fix: a transcript
// with ONLY built-in Read/Grep traffic (zero coverable shell commands) is
// exactly the gap the capture experiment measures and must appear in the
// session table, with shell adoption columns at zero.
func TestSessionsIncludesFileToolOnlySessions(t *testing.T) {
	base := t.TempDir()
	project := "/tmp/ftonly"
	dir := filepath.Join(base, "projects", encodeClaudeProjectSlug(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"assistant","timestamp":"2026-06-04T10:00:00Z","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}`,
		`{"type":"assistant","timestamp":"2026-06-04T10:01:00Z","message":{"content":[{"type":"tool_use","name":"Grep","input":{}}]}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "ft.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := Sessions(Options{ClaudeDirs: []string{base}, Project: project})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("got %d sessions, want the file-tool-only one", len(stats))
	}
	s := stats[0]
	if s.Coverable != 0 || s.Covered != 0 {
		t.Errorf("shell columns must stay zero, got coverable=%d covered=%d", s.Coverable, s.Covered)
	}
	if s.FileTools.Reads != 1 || s.FileTools.Greps != 1 {
		t.Errorf("FileTools = %+v, want Reads=1 Greps=1", s.FileTools)
	}
}
