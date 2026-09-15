package rewrite

import "testing"

// The match deliberately over-approximates Claude Code's worktree guard, which
// refuses a ctx-wire command whose argument merely contains the text git.
// Anything that names git as a word must match; words that only contain the
// letters must not.
func TestMentionsGit(t *testing.T) {
	yes := []string{
		"git status",
		"git status --short",
		"/usr/bin/git log",
		"command git status",
		"npm test && git diff",
		"git log --oneline | head -5",
		`rg -n "git status" .`,
		`ctx-wire explain "git status"`,
		"cat .git/HEAD",
		"git-lfs pull",
		`C:\\Program Files\\Git\\bin\\git.exe status`,
	}
	no := []string{
		"npm test",
		"gh pr list",
		"cat .gitignore",
		"open https://github.com/x",
		"echo digit legit",
		"",
	}
	for _, line := range yes {
		if !MentionsGit(line) {
			t.Errorf("MentionsGit(%q) = false, want true", line)
		}
	}
	for _, line := range no {
		if MentionsGit(line) {
			t.Errorf("MentionsGit(%q) = true, want false", line)
		}
	}
}

func TestUnwrapRunsStripsEveryWrapper(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		// The exact form the Claude hook emits, and the form agents type from
		// their instructions (which the hook would otherwise stamp).
		{"ctx-wire run --agent claude git status --short", "git status --short"},
		{"ctx-wire run git status", "git status"},
		// Every flag shape `ctx-wire run` accepts before the command.
		{"ctx-wire run --agent=claude git log", "git log"},
		{"ctx-wire run --no-dedup git diff", "git diff"},
		{"ctx-wire run --no-dedup --no-dedup --agent claude git diff", "git diff"},
		// The binary by path, with either separator and with .exe.
		{"/usr/local/bin/ctx-wire run git diff", "git diff"},
		{`C:\bin\ctx-wire.exe run git diff`, "git diff"},
		// Pipelines: the hook wraps the final stage.
		{"git log --oneline | ctx-wire run --agent claude head -5", "git log --oneline | head -5"},
		// Compound lines unwrap every segment, git or not.
		{
			"ctx-wire run --agent claude git status && ctx-wire run --agent claude ls",
			"git status && ls",
		},
		// Command prefixes stay in place.
		{"time ctx-wire run --agent claude git fetch", "time git fetch"},
		{"command ctx-wire run git status", "command git status"},
		// The command's own quoting and trailing text are untouched.
		{`ctx-wire run git commit -m "fix: a b"`, `git commit -m "fix: a b"`},
		{"  ctx-wire run git status  ", "  git status  "},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			if got := UnwrapRuns(c.line); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// Anything that is not a plain `ctx-wire run` wrapper is left exactly as typed.
func TestUnwrapRunsLeavesEverythingElseAlone(t *testing.T) {
	for _, line := range []string{
		"git status",                          // no wrapper
		"ctx-wire gain",                       // a different subcommand
		`ctx-wire explain "git status"`,       // mentions git, not a run
		"ctx-wire run --shim git status",      // internal re-entry flag
		"ctx-wire run --bogus git status",     // unknown flag
		"ctx-wire run",                        // no command
		"ctx-wire run --agent",                // flag without a value
		`ctx-wire run --agent "claude" git x`, // quoted token
		"my-ctx-wire run git status",          // a different binary
	} {
		t.Run(line, func(t *testing.T) {
			if got := UnwrapRuns(line); got != line {
				t.Errorf("rewritten to %q, want unchanged", got)
			}
		})
	}
}

// The hook answers "allow" for whatever it returns, so a line hiding a command
// it cannot attest must come back unchanged, the same rule lineWith follows.
func TestUnwrapRunsRefusesUnattestableLines(t *testing.T) {
	for _, line := range []string{
		"ctx-wire run git log $(cat ref)",
		"ctx-wire run git log `cat ref`",
		"ctx-wire run git status & rm -rf /tmp/x",
	} {
		if got := UnwrapRuns(line); got != line {
			t.Errorf("UnwrapRuns(%q) = %q, want unchanged", line, got)
		}
	}
}
