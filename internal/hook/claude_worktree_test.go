package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRepo lays out a main worktree and a linked worktree the way git does on
// disk, without running git: main/.git is a directory, and the linked
// worktree's .git is a file pointing into main/.git/worktrees/<name>. The Claude
// worktree layout (.claude/worktrees/<name>) is used so the fixture matches what
// EnterWorktree creates.
func fakeRepo(t *testing.T) (main, linked string) {
	t.Helper()
	root := t.TempDir()
	main = filepath.Join(root, "main")
	linked = filepath.Join(main, ".claude", "worktrees", "feat")
	for _, d := range []string{
		filepath.Join(main, ".git", "worktrees", "feat"),
		filepath.Join(main, "src"),
		filepath.Join(linked, "src"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitdir := "gitdir: " + filepath.Join(main, ".git", "worktrees", "feat") + "\n"
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte(gitdir), 0o644); err != nil {
		t.Fatal(err)
	}
	return main, linked
}

func TestInLinkedWorktree(t *testing.T) {
	main, linked := fakeRepo(t)

	// A submodule also has a .git file, but it points into .git/modules/.
	sub := filepath.Join(main, "vendor", "lib")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../../.git/modules/lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A worktree whose gitdir was written with Windows separators.
	winWT := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(winWT, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winWT, ".git"), []byte(`gitdir: C:\repo\.git\worktrees\wt`+"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"main worktree root", main, false},
		{"main worktree subdir", filepath.Join(main, "src"), false},
		{"linked worktree root", linked, true},
		{"linked worktree subdir", filepath.Join(linked, "src"), true},
		{"submodule", sub, false},
		{"windows-style gitdir", winWT, true},
		{"not a repository", t.TempDir(), false},
		{"empty cwd", "", false},
		{"relative cwd", "src", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := inLinkedWorktree(c.dir); got != c.want {
				t.Errorf("inLinkedWorktree(%q) = %v, want %v", c.dir, got, c.want)
			}
		})
	}
}

// runClaudeHook sends a Bash PreToolUse payload for command in cwd and returns
// the rewritten command, or "" when the hook stayed silent. Permission rules are
// read from an empty temp config dir so the developer's own settings cannot
// change the outcome.
func runClaudeHook(t *testing.T, cwd, command string) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	payload, err := json.Marshal(map[string]any{
		"session_id":      "s",
		"cwd":             cwd,
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Claude(bytes.NewReader(payload), &out); err != nil {
		t.Fatalf("Claude: %v", err)
	}
	if out.Len() == 0 {
		return ""
	}
	var got claudeOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if got.HookSpecificOutput.UpdatedInput == nil {
		t.Fatalf("hook answered without an updated command: %s", out.String())
	}
	return got.HookSpecificOutput.UpdatedInput.Command
}

// The reported bug: inside an isolated worktree, Claude Code refuses the
// rewritten `ctx-wire run --agent claude git status`, so every git command in
// the session failed, `git status` included. The hook must leave it alone.
func TestClaudeHookLeavesGitUnwrappedInLinkedWorktree(t *testing.T) {
	_, linked := fakeRepo(t)
	for _, cmd := range []string{
		"git status",
		"git status --short",
		"git log --oneline | head -5",
		"ls && git diff",
		`ls -la ".git"`,
	} {
		t.Run(cmd, func(t *testing.T) {
			if got := runClaudeHook(t, linked, cmd); got != "" {
				t.Errorf("rewritten to %q; the worktree guard refuses ctx-wire alongside git", got)
			}
		})
	}
}

// An agent following its ctx-wire instructions types the wrapper itself. In an
// isolated worktree that command cannot run, so the hook removes the wrapper.
func TestClaudeHookUnwrapsTypedGitRunsInLinkedWorktree(t *testing.T) {
	_, linked := fakeRepo(t)
	cases := []struct{ cmd, want string }{
		{"ctx-wire run git status --short", "git status --short"},
		{"ctx-wire run --agent claude git log -3", "git log -3"},
		{"git log | ctx-wire run head -5", "git log | head -5"},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			if got := runClaudeHook(t, linked, c.cmd); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Only lines that mention git change inside a worktree. The reporter confirmed
// npm, tsc and next build ran fine wrapped, so filtering them stays on.
func TestClaudeHookStillWrapsNonGitInLinkedWorktree(t *testing.T) {
	_, linked := fakeRepo(t)
	if got := runClaudeHook(t, linked, "ls"); !strings.HasPrefix(got, "ctx-wire run --agent claude ls") {
		t.Errorf("ls in a worktree = %q, want it wrapped as before", got)
	}
}

// Outside a linked worktree nothing changes: git is wrapped exactly as before.
func TestClaudeHookWrapsGitInMainWorktree(t *testing.T) {
	main, _ := fakeRepo(t)
	if got, want := runClaudeHook(t, main, "git status"), "ctx-wire run --agent claude git status"; got != want {
		t.Errorf("git status in the main worktree = %q, want %q", got, want)
	}
}

// Gitfile shapes beyond the default absolute path. Git writes a RELATIVE gitdir
// when worktree.useRelativePaths is set, and a .git file can be malformed,
// oversized, or unreadable. Every case the detector cannot positively read as a
// linked worktree must answer false, so the hook keeps its normal behavior.
func TestInLinkedWorktreeGitfileShapes(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"relative worktree gitdir", "gitdir: ../main/.git/worktrees/feat\n", true},
		{"no trailing newline", "gitdir: /repo/.git/worktrees/feat", true},
		{"no gitdir line", "not a gitfile\n", false},
		{"empty file", "", false},
		{"gitdir with no path", "gitdir:\n", false},
		{"relative submodule gitdir", "gitdir: ../.git/modules/lib\n", false},
		// The detector reads at most 4 KiB; a gitdir line past that is never
		// seen, and a file that large is not a real gitfile anyway.
		{"gitdir beyond the read cap", strings.Repeat("x", 5000) + "\ngitdir: /repo/.git/worktrees/feat\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := inLinkedWorktree(write(t, c.body)); got != c.want {
				t.Errorf("inLinkedWorktree = %v, want %v for gitfile %q", got, c.want, c.body)
			}
		})
	}
}

func TestInLinkedWorktreeUnreadableGitfile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of mode")
	}
	dir := t.TempDir()
	gitfile := filepath.Join(dir, ".git")
	if err := os.WriteFile(gitfile, []byte("gitdir: /repo/.git/worktrees/feat\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(gitfile); err == nil {
		t.Skip("this platform ignores file modes")
	}
	if inLinkedWorktree(dir) {
		t.Error("an unreadable gitfile was treated as a linked worktree")
	}
}

// A submodule or nested repository INSIDE a linked worktree has its own .git
// marker. The session is still isolated in the outer worktree, so detection must
// look past that marker. Stopping at the nearest .git answered false here, and
// the hook wrapped git again.
func TestInLinkedWorktreeLooksPastNestedRepos(t *testing.T) {
	main, linked := fakeRepo(t)

	mkGitfile := func(t *testing.T, dir, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkGitDir := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	subInLinked := filepath.Join(linked, "vendor", "lib")
	mkGitfile(t, subInLinked, "gitdir: ../../.git/modules/lib\n")
	nestedInLinked := filepath.Join(linked, "tools", "inner")
	mkGitDir(t, nestedInLinked)
	subInMain := filepath.Join(main, "vendor", "lib")
	mkGitfile(t, subInMain, "gitdir: ../../.git/modules/lib\n")
	nestedInMain := filepath.Join(main, "tools", "inner")
	mkGitDir(t, nestedInMain)

	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"submodule inside linked worktree", subInLinked, true},
		{"submodule subdir inside linked worktree", filepath.Join(subInLinked, "src"), true},
		{"nested repo inside linked worktree", nestedInLinked, true},
		{"submodule inside main worktree", subInMain, false},
		{"nested repo inside main worktree", nestedInMain, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.MkdirAll(c.dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if got := inLinkedWorktree(c.dir); got != c.want {
				t.Errorf("inLinkedWorktree(%q) = %v, want %v", c.dir, got, c.want)
			}
		})
	}
}

// The same case end to end: git run from a submodule inside an isolated worktree
// must not be wrapped.
func TestClaudeHookLeavesGitUnwrappedInSubmoduleOfLinkedWorktree(t *testing.T) {
	_, linked := fakeRepo(t)
	sub := filepath.Join(linked, "vendor", "lib")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../../.git/modules/lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runClaudeHook(t, sub, "git status"); got != "" {
		t.Errorf("rewritten to %q from a submodule inside a linked worktree", got)
	}
}
