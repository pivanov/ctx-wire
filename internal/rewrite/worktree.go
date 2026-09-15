package rewrite

import (
	"regexp"
	"strings"
)

// Claude Code's worktree isolation (tightened across 2.1.256 to 2.1.272)
// refuses a Bash command that runs git through a launcher it cannot read. It
// checks the command AFTER a PreToolUse hook has rewritten it, so our own
// `ctx-wire run --agent claude git status` is what it sees, and it refuses every
// git command in an isolated session, including `git status`. Verified live on
// 2.1.273: every wrapped form is refused, with or without --agent, and so is a
// ctx-wire command whose argument merely contains the TEXT git (reported with
// `ctx-wire explain "git status"`).
//
// The match here deliberately OVER-approximates the guard rather than modelling
// it. The guard allows ctx-wire on the same line as git when git is not one of
// ctx-wire's arguments (`git log | ctx-wire run head` runs), but a whole-line
// textual match skips that line too. The two failure modes are not symmetric:
// over-matching loses filtering on a few lines, under-matching leaves the
// session unable to touch its own repository. Reported in pivanov/ctx-wire#5.

// gitWord matches git named as a whole word anywhere in a line: `git status`,
// `/usr/bin/git log`, `rg "git status"`, `.git/HEAD`. It does not match
// `github`, `.gitignore`, `digit` or `legit`.
var gitWord = regexp.MustCompile(`\bgit\b`)

// MentionsGit reports whether line names git as a whole word anywhere, in a
// command position or not.
func MentionsGit(line string) bool {
	return gitWord.MatchString(line)
}

// UnwrapRuns removes every `ctx-wire run` wrapper from line, in every top-level
// segment and pipeline stage, and returns the inner commands. The flags a wrapper
// may carry are exactly those `ctx-wire run` accepts before the command: any
// number of --no-dedup, then one optional --agent <name> or --agent=<name>. A
// wrapper with anything else (the internal --shim, an unknown flag, a quoted
// token) is left alone.
//
// It exists for the case above: an agent following its ctx-wire instructions
// types `ctx-wire run git status` itself, and inside an isolated worktree that
// can never run. Dropping the wrapper runs the same command, only unfiltered.
//
// A line that hides a command we cannot attest (command substitution, a
// smuggled newline or background &) is returned unchanged, for the same reason
// lineWith refuses to rewrite one: the hook answers "allow", and it must not
// vouch for a construct it never inspected.
func UnwrapRuns(line string) string {
	if ContainsUnattestableConstruct(line) {
		return line
	}
	segments, seps := splitTopLevel(line)
	for i, seg := range segments {
		segments[i] = unwrapSegment(seg)
	}
	var b strings.Builder
	for i, seg := range segments {
		b.WriteString(seg)
		if i < len(seps) {
			b.WriteString(seps[i])
		}
	}
	return b.String()
}

// unwrapSegment unwraps each stage of a (possibly piped) segment.
func unwrapSegment(seg string) string {
	if idx := lastTopLevelPipe(seg); idx >= 0 {
		return unwrapSegment(seg[:idx]) + "|" + unwrapStage(seg[idx+1:])
	}
	return unwrapStage(seg)
}

// unwrapStage strips a wrapper from one command, keeping any command prefix
// (time, env assignments, command) and the stage's surrounding whitespace.
func unwrapStage(stage string) string {
	core := strings.TrimLeft(stage, " \t")
	if core == "" {
		return stage
	}
	lead := stage[:len(stage)-len(core)]
	start, reason := peelPrefix(core)
	if reason != "" {
		return stage
	}
	inner, ok := stripRunWrapper(core[start:])
	if !ok {
		return stage
	}
	return lead + core[:start] + inner
}

// stripRunWrapper returns the command inside `ctx-wire run [flags] <cmd>`, with
// the command's own text (quoting, trailing whitespace) untouched.
func stripRunWrapper(s string) (string, bool) {
	tok, rest, ok := leadingToken(s)
	if !ok || !isCtxWireBinary(tok) {
		return "", false
	}
	if tok, rest, ok = leadingToken(rest); !ok || tok != "run" {
		return "", false
	}
	tok, next, ok := leadingToken(rest)
	for ok && tok == "--no-dedup" {
		rest = next
		tok, next, ok = leadingToken(rest)
	}
	switch {
	case ok && tok == "--agent":
		if _, afterValue, valueOK := leadingToken(next); valueOK {
			rest = afterValue
		} else {
			return "", false
		}
	case ok && strings.HasPrefix(tok, "--agent=") && len(tok) > len("--agent="):
		rest = next
	}
	cmd := strings.TrimLeft(rest, " \t")
	if cmd == "" || strings.HasPrefix(cmd, "-") {
		return "", false
	}
	return cmd, true
}

// leadingToken splits off the first whitespace-delimited token of s. A token
// carrying quotes is refused, since unwrapping must never reinterpret quoting.
func leadingToken(s string) (tok, rest string, ok bool) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return "", "", false
	}
	end := strings.IndexAny(s, " \t")
	if end < 0 {
		end = len(s)
	}
	tok = s[:end]
	if strings.ContainsAny(tok, `"'`) {
		return "", "", false
	}
	return tok, s[end:], true
}

// isCtxWireBinary reports whether tok names the ctx-wire binary, bare or by path,
// with either separator and with or without .exe.
func isCtxWireBinary(tok string) bool {
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		tok = tok[i+1:]
	}
	return tok == "ctx-wire" || tok == "ctx-wire.exe"
}
