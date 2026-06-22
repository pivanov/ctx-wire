package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ctx-wire/internal/filter"
)

func mustReg(t *testing.T) *filter.Registry {
	t.Helper()
	reg, err := filter.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	return reg
}

// writeClaudeSession writes a Claude transcript jsonl under
// base/projects/<slug>/session.jsonl with one assistant Bash tool_use per cmd.
func writeClaudeSession(t *testing.T, base, project string, cmds ...string) {
	t.Helper()
	dir := filepath.Join(base, "projects", encodeClaudeProjectSlug(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, c := range cmds {
		line := map[string]any{
			"type":      "assistant",
			"timestamp": "2026-05-31T10:00:00.000Z",
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": c}},
				},
			},
		}
		if err := enc.Encode(line); err != nil {
			t.Fatal(err)
		}
	}
}

// writeCodexSession writes a Codex rollout jsonl with one exec_command per cmd.
func writeCodexSession(t *testing.T, codexHome, workdir string, cmds ...string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "05", "31")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "rollout-2026-05-31T10-00-00-test.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, c := range cmds {
		args, _ := json.Marshal(map[string]string{"cmd": c, "workdir": workdir})
		line := map[string]any{
			"type":      "response_item",
			"timestamp": "2026-05-31T10:00:00.000Z",
			"payload":   map[string]any{"type": "function_call", "name": "exec_command", "arguments": string(args)},
		}
		if err := enc.Encode(line); err != nil {
			t.Fatal(err)
		}
	}
}

// setGain points the gain log at a temp file and writes the given scrubbed
// commands as captured records.
func setGain(t *testing.T, captured ...string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv("CTX_WIRE_GAIN_FILE", path)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, c := range captured {
		if err := enc.Encode(map[string]any{
			"ts": "2026-05-31T10:00:01Z", "command": c, "mode": "filtered",
			"raw_bytes": 1000, "emitted_bytes": 100, "saved_bytes": 900, "exit_code": 0,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEncodeClaudeProjectSlug(t *testing.T) {
	cases := map[string]string{
		"/Users/foo/bar":        "-Users-foo-bar",
		"/Users/first.last/bar": "-Users-first-last-bar",
		"/home/chris/2_project": "-home-chris-2-project",
		"/Users/pivanov/workspace/pivanov/packages/ctx-wire": "-Users-pivanov-workspace-pivanov-packages-ctx-wire",
	}
	for in, want := range cases {
		if got := encodeClaudeProjectSlug(in); got != want {
			t.Errorf("encodeClaudeProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnalyzeClassifies(t *testing.T) {
	project := "/work/myproj"
	claudeBase := t.TempDir()
	codexHome := t.TempDir()
	// gain has "git status" and "git log --oneline" as captured.
	setGain(t, "git status", "git log --oneline")

	writeClaudeSession(t, claudeBase, project,
		"git status",        // filterable + in gain -> captured
		"frobnicate --all",  // filterable + not in gain -> escaped
		"vim main.go",       // interactive bypass -> hook_limited
		"cat log > out.txt", // redirection -> passthrough by design
	)
	writeCodexSession(t, codexHome, project,
		"git log --oneline", // captured
		"rtk git status",    // ran via RTK, not in gain -> escaped
	)

	rep, err := Analyze(mustReg(t), Options{ClaudeDirs: []string{claudeBase}, CodexDir: codexHome, Project: project})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Total != 6 {
		t.Fatalf("total = %d, want 6 (%+v)", rep.Total, rep.ByCategory)
	}
	checks := map[Category]int{
		CatCovered:     2, // git status, git log --oneline
		CatEscaped:     2, // frobnicate, rtk git status
		CatHookLimited: 1, // vim
		CatPassthrough: 1, // cat log > out.txt
	}
	for cat, want := range checks {
		if rep.ByCategory[cat] != want {
			t.Errorf("category %s = %d, want %d (all: %+v)", cat, rep.ByCategory[cat], want, rep.ByCategory)
		}
	}
	// Escaped rows must include the raw frobnicate and the rtk command.
	var escaped []string
	for _, r := range rep.Escaped {
		escaped = append(escaped, r.Command)
	}
	if len(rep.Escaped) != 2 {
		t.Fatalf("expected 2 escaped rows, got %v", escaped)
	}
}

// TestQuotedAndPipedCommandsMatchGain guards the matching fix: a quoted command
// and a pipeline whose last stage was wrapped must correlate to their gain
// records, which store the de-quoted argv of the inner command.
func TestQuotedAndPipedCommandsMatchGain(t *testing.T) {
	project := "/work/myproj"
	claudeBase := t.TempDir()
	// gain stores the canonical (de-quoted, scrubbed) inner command, exactly as
	// scrub.Command produced it at run time.
	setGain(t, "sed -n 1,12p file.txt", "head -3")

	writeClaudeSession(t, claudeBase, project,
		"sed -n '1,12p' file.txt", // quoted in the transcript; de-quoted in gain
		"cat file.txt | head -3",  // pipeline; only the last stage is wrapped+recorded
	)
	rep, err := Analyze(mustReg(t), Options{ClaudeDirs: []string{claudeBase}, Project: project})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.ByCategory[CatCovered] != 2 {
		t.Fatalf("covered = %d, want 2 (%+v)", rep.ByCategory[CatCovered], rep.ByCategory)
	}
	if rep.ByCategory[CatEscaped] != 0 {
		t.Errorf("escaped = %d, want 0 (quoted/piped must not be false escapes)", rep.ByCategory[CatEscaped])
	}
}

// TestPredatesLedgerBucket guards the window fix: a command that ran well before
// the earliest gain record is bucketed apart from escaped.
func TestPredatesLedgerBucket(t *testing.T) {
	project := "/work/myproj"
	claudeBase := t.TempDir()
	// gain's only record is recent; the transcript command is from 2026-05-31
	// 10:00, far more than the grace window before it.
	path := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv("CTX_WIRE_GAIN_FILE", path)
	if err := os.WriteFile(path, []byte(`{"ts":"2026-06-05T12:00:00Z","command":"go test ./...","mode":"filtered","raw_bytes":1,"emitted_bytes":1,"saved_bytes":0,"exit_code":0}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, claudeBase, project, "frobnicate --all") // 2026-05-31, predates ledger
	rep, err := Analyze(mustReg(t), Options{ClaudeDirs: []string{claudeBase}, Project: project})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.ByCategory[CatPredatesLedger] != 1 {
		t.Fatalf("predates_ledger = %d, want 1 (%+v)", rep.ByCategory[CatPredatesLedger], rep.ByCategory)
	}
	if rep.ByCategory[CatEscaped] != 0 {
		t.Errorf("escaped = %d, want 0 (pre-ledger command must not count as escaped)", rep.ByCategory[CatEscaped])
	}
}

func TestProjectScoping(t *testing.T) {
	claudeBase := t.TempDir()
	setGain(t)
	writeClaudeSession(t, claudeBase, "/work/myproj", "frobnicate a")
	writeClaudeSession(t, claudeBase, "/work/other", "frobnicate b")

	// Scoped to myproj: only sees frobnicate a.
	scoped, err := Analyze(mustReg(t), Options{ClaudeDirs: []string{claudeBase}, Project: "/work/myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Total != 1 {
		t.Fatalf("scoped total = %d, want 1", scoped.Total)
	}
	// --all (Project empty): sees both.
	all, err := Analyze(mustReg(t), Options{ClaudeDirs: []string{claudeBase}})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 2 {
		t.Fatalf("all total = %d, want 2", all.Total)
	}
}

func TestCodexWorkdirScoping(t *testing.T) {
	codexHome := t.TempDir()
	setGain(t)
	writeCodexSession(t, codexHome, "/work/elsewhere", "frobnicate x")
	rep, err := Analyze(mustReg(t), Options{CodexDir: codexHome, Project: "/work/myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 0 {
		t.Fatalf("codex command in another project should be excluded, got %d", rep.Total)
	}
}

func TestCodexArgvForm(t *testing.T) {
	cmd, wd := codexCommand(`{"command":["bash","-lc","git status"],"workdir":"/p"}`)
	if cmd != "git status" || wd != "/p" {
		t.Fatalf("argv form: cmd=%q wd=%q", cmd, wd)
	}
	cmd2, _ := codexCommand(`{"cmd":"ls -la","workdir":"/p"}`)
	if cmd2 != "ls -la" {
		t.Fatalf("cmd form: %q", cmd2)
	}
}

func TestAnalyzeWritesNothing(t *testing.T) {
	claudeBase := t.TempDir()
	codexHome := t.TempDir()
	setGain(t, "git status")
	writeClaudeSession(t, claudeBase, "/work/p", "git status", "frobnicate")
	probe := t.TempDir()
	before, _ := os.ReadDir(probe)
	if _, err := Analyze(mustReg(t), Options{ClaudeDirs: []string{claudeBase}, CodexDir: codexHome, Project: "/work/p"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadDir(probe)
	if len(after) != len(before) {
		t.Fatalf("Analyze must not write files")
	}
}

func TestEmptyReport(t *testing.T) {
	setGain(t)
	rep, err := Analyze(mustReg(t), Options{ClaudeDirs: []string{t.TempDir()}, CodexDir: t.TempDir(), Project: "/nope"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 0 {
		t.Fatalf("expected empty report, got %d", rep.Total)
	}
	out := Format(rep, Options{})
	if out == "" {
		t.Fatal("expected non-empty formatted output")
	}
}
