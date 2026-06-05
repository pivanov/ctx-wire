package discover

import (
	"os"
	"path/filepath"
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
