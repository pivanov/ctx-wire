// Package rewrite turns a shell command line into one that routes simple
// commands through `ctx-wire run`, so agent hooks can transparently capture
// token savings. Compound commands (joined by ; && || outside groups) are split
// and each rewritable segment is wrapped. Command prefixes (env assignments,
// `env <assignments>`, `command`, `time`) are peeled so the inner command is
// still wrapped and filtered. For a pipeline, only the FINAL stage is wrapped
// (producers run raw, so consumers like wc/grep/jq still see the true stream and
// only the agent-facing final output is filtered); if the last stage is not
// wrappable the whole pipeline passes through. Redirections, shell builtins,
// control keywords, subshells/brace groups, and already-wrapped commands pass
// through untouched.
//
// This is a conservative, POSIX-ish recognizer, not a full shell parser: when a
// construct cannot be wrapped safely it is left alone. ctx-wire explain reports
// the exact decision and reason for any command line.
package rewrite

import (
	"os/exec"
	"path/filepath"
	"strings"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/commandpolicy"
)

const prefix = "ctx-wire run "

// lookPath reports whether name resolves to a runnable executable. It is a var
// so tests can stub PATH resolution deterministically. A command that does not
// resolve is almost always a shell function, alias, or builtin defined in the
// caller's shell, which a separate `ctx-wire run` process cannot exec. Wrapping
// one would turn a working command into "command not found", so such commands
// must pass through untouched. ctx-wire is meant to observe commands, never to
// break them.
var lookPath = func(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// shellBuiltins must run in the shell process itself; wrapping them in a child
// process would be a no-op or wrong, so they are never rewritten.
var shellBuiltins = map[string]bool{
	"cd": true, "export": true, "unset": true, "set": true, "source": true,
	".": true, ":": true, "alias": true, "unalias": true, "eval": true,
	"exec": true, "pushd": true, "popd": true, "umask": true, "trap": true,
	"local": true, "declare": true, "typeset": true, "let": true, "return": true,
	"shift": true, "read": true, "wait": true, "jobs": true, "fg": true,
	"bg": true, "disown": true, "hash": true, "type": true, "command": true,
	"builtin": true, "history": true, "ulimit": true, "test": true,
	"[": true, "[[": true, "echo": true, "printf": true,
}

// shellKeywords are reserved control words. A segment beginning with one is shell
// control syntax (e.g. `for f in ...; do ...; done`), not a command, so wrapping
// it in `ctx-wire run` would be a syntax error. Leave such segments untouched.
var shellKeywords = map[string]bool{
	"for": true, "do": true, "done": true, "if": true, "then": true,
	"elif": true, "else": true, "fi": true, "while": true, "until": true,
	"case": true, "esac": true, "select": true, "in": true, "function": true,
	"time": true, "coproc": true, "repeat": true,
}

// Line rewrites a shell command line. If nothing is rewritable it returns the
// input unchanged (callers treat an unchanged result as a no-op passthrough).
func Line(line string) string {
	return lineWith(line, prefix)
}

// LineForAgent rewrites a shell command line and marks wrapped ctx-wire runs
// with the invoking agent. The explicit ctx-wire flag is more reliable than
// process-tree detection for plugins, MCP hosts, and agents that launch shell
// commands through helper processes.
func LineForAgent(line, agentName string) string {
	wrap := wrapForAgent(agentName)
	if wrap == prefix {
		return Line(line)
	}
	return lineWith(line, wrap)
}

func wrapForAgent(agentName string) string {
	name := agent.Normalize(agentName)
	if name == "" {
		return prefix
	}
	return "ctx-wire run --agent " + name + " "
}

// lineWith rewrites a command line using wrap as the wrap prefix for each
// rewritable segment (the default "ctx-wire run ", or an agent-scoped variant).
func lineWith(line, wrap string) string {
	// Safety gate: never rewrite a line that hides a command we cannot attest
	// (command/process substitution, or an extra top-level command smuggled in
	// via a newline or background &). Returning it unchanged makes every hook
	// fall through to passthrough, so the agent evaluates the original command
	// instead of ctx-wire vouching for a construct it never inspected. Plain
	// redirects are not gated here; they hide no command and pass through anyway.
	if ContainsUnattestableConstruct(line) {
		return line
	}
	segments, seps := splitTopLevel(line)
	for i, seg := range segments {
		segments[i] = rewriteSegment(seg, wrap)
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

// rewriteSegment wraps a single command segment, preserving leading/trailing
// whitespace. The decision (including the `time` command-prefix case) is made by
// rewriteCore so Line and Explain never diverge.
func rewriteSegment(seg, wrap string) string {
	core := strings.TrimSpace(seg)
	if core == "" {
		return seg
	}
	rewritten, inner, wrapped, _ := rewriteCore(core, wrap)
	if !wrapped {
		return seg
	}
	// Runtime safety gate (Line only, not Explain): only wrap a command that
	// actually resolves to an executable. A first token that is not on PATH is a
	// shell function, alias, or typo that a separate `ctx-wire run` process
	// cannot exec, so wrapping it would turn a working command into "command not
	// found". ctx-wire observes commands, it must never break them. This check
	// is host-dependent, so it lives here rather than in passReason, which stays
	// a pure shape classifier shared with Explain and discover.
	if !lookPath(firstToken(inner)) {
		return seg
	}
	lead := seg[:len(seg)-len(strings.TrimLeft(seg, " \t"))]
	trail := seg[len(strings.TrimRight(seg, " \t")):]
	return lead + rewritten + trail
}

// rewriteCore decides how a single trimmed command core is rewritten. It returns
// the rewritten core, the inner command that `ctx-wire run` actually receives
// (for the runner-side decision), whether anything was wrapped, and (when not)
// the passthrough reason. It is the single source of truth shared by Line and
// Explain.
//
// `time` (and `time -p`) is a command prefix, not a command: it runs and times
// the following command. Wrapping `time` itself would try to exec a `time`
// program rather than use the shell keyword, so instead the timed command is
// rewritten: `time go test` -> `time ctx-wire run go test`.
func rewriteCore(core, wrap string) (rewritten, inner string, wrapped bool, reason string) {
	if core == "" {
		return core, "", false, ReasonEmpty
	}
	// Pipelines: wrap only the FINAL stage. The producers run raw so consumers
	// like wc/grep/jq/sort still see the true stream; only the agent-facing final
	// output is filtered. e.g. `rg TODO . | wc -l` -> `rg TODO . | ctx-wire run
	// wc -l`. If the last stage is not wrappable, the whole pipeline passes
	// through unchanged (the conservative default).
	if idx := lastTopLevelPipe(core); idx >= 0 {
		return rewritePipeline(core, idx, wrap)
	}
	// Subshells and brace groups are shell grouping syntax, not commands; wrapping
	// them would be a syntax error. Leave untouched.
	if strings.HasPrefix(core, "(") {
		return core, "", false, ReasonSubshell
	}
	if strings.HasPrefix(core, "{") {
		return core, "", false, ReasonBraceGroup
	}
	// Command prefixes (time, env assignments, `env <assignments>`, `command`)
	// run a following command; peel the prefix and rewrite the inner command so
	// it still gets filtered, e.g. `FOO=bar git status` -> `FOO=bar ctx-wire run
	// git status`.
	innerStart, pr := peelPrefix(core)
	if pr != "" {
		return core, "", false, pr // prefix is itself terminal/unsafe -> passthrough
	}
	if innerStart > 0 {
		pfx := core[:innerStart]
		rest := core[innerStart:]
		innerRewritten, innerInner, innerWrapped, innerReason := rewriteCore(rest, wrap)
		if innerWrapped {
			return pfx + innerRewritten, innerInner, true, ""
		}
		return core, "", false, innerReason
	}
	if rewritten, inner, changed, detected := rewriteShellCommandString(core, wrap); detected {
		if !changed {
			return core, "", false, ReasonShellCommandString
		}
		return rewritten, inner, true, ""
	}
	// An explicitly typed `ctx-wire run <cmd>` (the form agent instructions
	// teach) is already wrapped but carries no attribution, and inside a
	// sandboxed agent shell the process-tree fallback is blind, so the savings
	// land as (unattributed). When this rewrite runs for a known agent, stamp
	// the agent in instead of passing the line through.
	if stamped, inner, ok := stampWrappedAgent(core, wrap); ok {
		return stamped, inner, true, ""
	}
	if r := passReason(core); r != "" {
		return core, "", false, r
	}
	return wrap + core, core, true, ""
}

// stampWrappedAgent rewrites `ctx-wire run <cmd>` to `ctx-wire run --agent
// <name> <cmd>` when wrap is an agent-scoped variant. Fail-open by shape: it
// only fires for a bare `run` immediately followed by a non-flag token, so an
// explicit `--agent` (the user's choice wins), `--shim`, or any unknown flag
// order passes through untouched. The ctx-wire binary may be path-qualified;
// the path form is preserved.
func stampWrappedAgent(core, wrap string) (stamped, inner string, ok bool) {
	if wrap == prefix || !strings.HasPrefix(wrap, "ctx-wire run --agent ") {
		return "", "", false // no agent to stamp
	}
	first := firstToken(core)
	if filepath.Base(first) != "ctx-wire" {
		return "", "", false
	}
	rest := strings.TrimSpace(core[len(first):])
	if !strings.HasPrefix(rest, "run ") {
		return "", "", false // gain/doctor/other subcommands are not runs
	}
	cmd := strings.TrimSpace(rest[len("run "):])
	if cmd == "" || strings.HasPrefix(cmd, "-") {
		return "", "", false // flags present: leave the invocation alone
	}
	agentFlags := strings.TrimSuffix(strings.TrimPrefix(wrap, "ctx-wire run "), " ")
	return first + " run " + agentFlags + " " + cmd, cmd, true
}

// rewritePipeline wraps the final stage of a pipeline whose last top-level pipe
// is at byte index idx. The head (all producers plus the trailing pipe) is kept
// verbatim so intermediate data is unchanged; only the last stage is rewritten.
// If the last stage is not wrappable (builtin, redirect, subshell, etc.), the
// whole pipeline passes through unchanged.
func rewritePipeline(core string, idx int, wrap string) (rewritten, inner string, wrapped bool, reason string) {
	head := core[:idx+1] // producers and the final '|'
	lastStage := core[idx+1:]
	lsCore := strings.TrimSpace(lastStage)
	if lsCore == "" {
		return core, "", false, ReasonPipeline
	}
	lr, lInner, lWrapped, _ := rewriteCore(lsCore, wrap)
	if !lWrapped {
		return core, "", false, ReasonPipeline
	}
	lead := lastStage[:len(lastStage)-len(strings.TrimLeft(lastStage, " \t"))]
	trail := lastStage[len(strings.TrimRight(lastStage, " \t")):]
	return head + lead + lr + trail, lInner, true, ""
}

// lastTopLevelPipe returns the byte index of the last single `|` at the top
// level of s (outside quotes, subshells, and brace groups, and not part of
// `||`), or -1 when there is none.
func lastTopLevelPipe(s string) int {
	last := -1
	var quote byte
	esc := false
	paren, brace := 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case quote == '\'':
			// single quotes are literal; backslash does not escape inside them
			if c == '\'' {
				quote = 0
			}
		case c == '\\':
			esc = true
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(':
			paren++
		case c == ')':
			if paren > 0 {
				paren--
			}
		case c == '{':
			brace++
		case c == '}':
			if brace > 0 {
				brace--
			}
		case c == '|' && paren == 0 && brace == 0:
			if i+1 < len(s) && s[i+1] == '|' {
				i++ // consume the second '|' so '||' is not treated as a pipe
				continue
			}
			last = i
		}
	}
	return last
}

// peelPrefix inspects the leading command-prefix of core (quote-aware). It
// returns innerStart>0 (byte offset where the inner command begins) when a
// prefix should be peeled and the remainder rewritten; or a non-empty reason
// when the leading construct is terminal/unsafe and the whole segment should
// pass through; or (0, "") when there is no peelable prefix and normal
// classification applies. The returned prefix slice core[:innerStart] preserves
// the exact original spacing and quoting.
func peelPrefix(core string) (innerStart int, reason string) {
	// Configured wrapper prefixes (e.g. "docker exec web") are peeled first so
	// the inner command is the one rewritten and filtered.
	if i, ok := commandpolicy.TransparentPrefix(core); ok {
		return i, ""
	}
	toks := scanTokens(core)
	if len(toks) == 0 {
		return 0, ""
	}

	switch toks[0].text {
	case "time":
		// time / time -p <cmd>
		i := 1
		if i < len(toks) && toks[i].text == "-p" {
			i++
		}
		if i >= len(toks) {
			return 0, ReasonShellKeyword + " time" // bare `time`
		}
		return toks[i].start, ""

	case "command":
		// `command <cmd>` runs cmd bypassing functions/aliases -> peel.
		// `command -v/-V/-p ...` (and any flag) looks up / changes behavior -> passthrough.
		if len(toks) == 1 {
			return 0, "" // bare `command` -> normal classification (builtin)
		}
		if strings.HasPrefix(toks[1].text, "-") {
			return 0, ReasonCommandLookup
		}
		return toks[1].start, ""

	case "env":
		// `env [NAME=val ...] <cmd>` -> peel. `env -flag ...` -> passthrough.
		i := 1
		for i < len(toks) && isEnvAssignment(toks[i].text) {
			i++
		}
		if i < len(toks) && strings.HasPrefix(toks[i].text, "-") {
			return 0, ReasonEnvFlags
		}
		if i >= len(toks) {
			return 0, "" // bare `env` or `env NAME=val` (no command) -> normal classification
		}
		return toks[i].start, ""
	}

	// Leading bare assignments: FOO=bar [BAR=x ...] <cmd>
	if isEnvAssignment(toks[0].text) {
		i := 0
		for i < len(toks) && isEnvAssignment(toks[i].text) {
			i++
		}
		if i >= len(toks) {
			return 0, ReasonEnvAssignment // assignments only, no command
		}
		return toks[i].start, ""
	}

	return 0, ""
}

// postoken is a token with its byte offset in the original string.
type postoken struct {
	text  string
	start int
	end   int
}

// scanTokens splits s into whitespace-delimited tokens, honoring single quotes,
// double quotes, and backslash escapes so that, e.g., `FOO="a b"` is one token.
// Each token records its starting byte offset so callers can slice the original
// string and preserve exact spacing/quoting.
func scanTokens(s string) []postoken {
	var toks []postoken
	i, n := 0, len(s)
	for i < n {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		start := i
		var b strings.Builder
		var quote byte
	scan:
		for i < n {
			c := s[i]
			switch {
			case quote != 0:
				b.WriteByte(c)
				if c == quote {
					quote = 0
				}
				i++
			case c == '\\' && i+1 < n:
				b.WriteByte(c)
				b.WriteByte(s[i+1])
				i += 2
			case c == '\'' || c == '"':
				quote = c
				b.WriteByte(c)
				i++
			case c == ' ' || c == '\t':
				break scan
			default:
				b.WriteByte(c)
				i++
			}
		}
		toks = append(toks, postoken{text: b.String(), start: start, end: i})
	}
	return toks
}

func rewriteShellCommandString(core, wrap string) (rewritten, inner string, changed, detected bool) {
	toks := scanTokens(core)
	if len(toks) < 3 || !isShellProgram(toks[0].text) {
		return "", "", false, false
	}
	for i := 1; i < len(toks)-1; i++ {
		tok := toks[i].text
		if tok == "--" {
			return "", "", false, false
		}
		if !strings.HasPrefix(tok, "-") {
			return "", "", false, false
		}
		if shellOptionTakesArg(tok) {
			i++
			continue
		}
		if isShellCommandFlag(tok) {
			command, quote, quoted := shellUnquoteToken(toks[i+1].text)
			if !quoted {
				return "", "", false, true
			}
			if hasDynamicShellExpansion(command) {
				return "", command, false, true
			}
			commandRewritten := lineWith(command, wrap)
			if commandRewritten == command {
				return "", command, false, true
			}
			requoted := shellSingleQuote(commandRewritten)
			if quote == '"' && !hasDynamicShellExpansion(commandRewritten) {
				requoted = shellDoubleQuote(commandRewritten)
			}
			return core[:toks[i+1].start] + requoted + core[toks[i+1].end:], command, true, true
		}
	}
	return "", "", false, false
}

func isShellCommandFlag(tok string) bool {
	if !strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "--") {
		return false
	}
	return strings.Contains(tok[1:], "c")
}

func shellOptionTakesArg(tok string) bool {
	switch tok {
	case "-o", "+o", "-O", "+O", "--init-file", "--rcfile":
		return true
	default:
		return false
	}
}

func isShellProgram(tok string) bool {
	switch filepath.Base(tok) {
	case "bash", "sh", "zsh":
		return true
	default:
		return false
	}
}

func shellUnquoteToken(tok string) (string, byte, bool) {
	if len(tok) < 2 {
		return "", 0, false
	}
	q := tok[0]
	if tok[len(tok)-1] != q || (q != '\'' && q != '"') {
		return "", 0, false
	}
	body := tok[1 : len(tok)-1]
	if q == '\'' {
		return body, q, true
	}
	var b strings.Builder
	escaped := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		b.WriteByte(c)
	}
	if escaped {
		b.WriteByte('\\')
	}
	return b.String(), q, true
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// ShellSingleQuote single-quotes s for POSIX shells (embedded single quotes
// become '\”). Exported for the hook adapters that build suggested commands.
func ShellSingleQuote(s string) string { return shellSingleQuote(s) }

func shellDoubleQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '"', '$', '`':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

func isDynamicCommandToken(tok string) bool {
	if tok == "" {
		return false
	}
	unquoted, _, quoted := shellUnquoteToken(tok)
	if quoted {
		tok = unquoted
	}
	return hasDynamicShellExpansion(tok)
}

func hasDynamicShellExpansion(s string) bool {
	return strings.ContainsAny(s, "$`")
}

// Reason codes for why a segment is left unwrapped. They are stable strings so
// callers (e.g. ctx-wire explain) can branch on them.
const (
	ReasonEmpty              = "empty command"
	ReasonAlreadyCtxWire     = "already a ctx-wire command"
	ReasonPipeline           = "pipeline"
	ReasonRedirection        = "redirection"
	ReasonShellBuiltin       = "shell builtin"
	ReasonShellKeyword       = "shell control keyword"
	ReasonEnvAssignment      = "env assignment prefix"
	ReasonSubshell           = "subshell"
	ReasonBraceGroup         = "brace group"
	ReasonCommandLookup      = "command builtin lookup/flags"
	ReasonEnvFlags           = "env with flags"
	ReasonShellCommandString = "shell command string"
	ReasonDynamicCommand     = "dynamic command"
	ReasonExecutableNotFound = "executable not found on PATH"
	ReasonExcluded           = "excluded by config"
	ReasonUnattestable       = "unattestable shell construct"
)

// passReason returns the reason a segment is left unwrapped (a Reason* constant,
// with the builtin name appended for ReasonShellBuiltin), or "" if the segment
// is rewritable. It is the single source of truth for the rewrite decision, so
// Line and Explain never diverge on which commands are wrapped.
func passReason(core string) string {
	if core == "" {
		return ReasonEmpty
	}
	if strings.HasPrefix(core, prefix) || strings.HasPrefix(core, "ctx-wire ") {
		return ReasonAlreadyCtxWire
	}
	// Redirections are left untouched: wrapping would route ctx-wire's filtered
	// output into the redirect target, changing what the user captures. Leave the
	// raw command in place. (Pipelines are handled earlier in rewriteCore, which
	// wraps only the final stage, so a top-level pipe never reaches here.)
	if containsTopLevelRedirect(core) {
		return ReasonRedirection
	}
	first := firstToken(core)
	if first == "" {
		return ReasonEmpty
	}
	if isDynamicCommandToken(first) {
		return ReasonDynamicCommand
	}
	if shellBuiltins[first] {
		return ReasonShellBuiltin + " " + first
	}
	if shellKeywords[first] {
		return ReasonShellKeyword + " " + first
	}
	if commandpolicy.IsExcluded(first) {
		return ReasonExcluded + " " + filepath.Base(first)
	}
	if filepath.Base(first) == "ctx-wire" {
		return ReasonAlreadyCtxWire
	}
	if isEnvAssignment(first) {
		return ReasonEnvAssignment // e.g. FOO=bar cmd; leave for a later phase
	}
	return ""
}

// firstToken returns the first whitespace-delimited token of s.
func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// isEnvAssignment reports whether tok looks like NAME=value.
func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range tok[:eq] {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// splitTopLevel splits line on top-level ; && || separators, returning the
// segments and the separators between them (len(seps) == len(segments)-1). It
// respects single/double quotes and backslash escapes. A single | is not a
// separator; it stays inside its segment and marks it as a pipeline.
func splitTopLevel(line string) (segments, seps []string) {
	var cur strings.Builder
	runes := []rune(line)
	var quote rune
	esc := false
	paren, brace := 0, 0 // nesting depth for (...) and {...}; never split inside
	clampDec := func(n int) int {
		if n > 0 {
			return n - 1
		}
		return 0
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case quote == '\'':
			// Single quotes are literal: a backslash does not escape inside
			// them, only the closing quote ends the string (POSIX). Checked
			// before the backslash case so `'...\'` closes correctly.
			cur.WriteRune(r)
			if r == '\'' {
				quote = 0
			}
		case r == '\\':
			cur.WriteRune(r)
			esc = true
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == '(':
			paren++
			cur.WriteRune(r)
		case r == ')':
			paren = clampDec(paren)
			cur.WriteRune(r)
		case r == '{':
			brace++
			cur.WriteRune(r)
		case r == '}':
			brace = clampDec(brace)
			cur.WriteRune(r)
		case (paren > 0 || brace > 0):
			// inside a subshell or brace group: do not split on operators
			cur.WriteRune(r)
		case r == ';':
			segments = append(segments, cur.String())
			cur.Reset()
			seps = append(seps, ";")
		case r == '&' && i+1 < len(runes) && runes[i+1] == '&':
			segments = append(segments, cur.String())
			cur.Reset()
			seps = append(seps, "&&")
			i++
		case r == '|' && i+1 < len(runes) && runes[i+1] == '|':
			segments = append(segments, cur.String())
			cur.Reset()
			seps = append(seps, "||")
			i++
		default:
			cur.WriteRune(r)
		}
	}
	segments = append(segments, cur.String())
	return segments, seps
}

// containsTopLevelRedirect reports whether s has a shell redirection operator
// (> or <, covering >>, 2>, 2>&1, &>, <, <<, etc.) outside quotes. A quoted
// angle bracket does not count.
func containsTopLevelRedirect(s string) bool {
	return hasUnquotedRune(s, func(r rune) bool { return r == '>' || r == '<' })
}

// hasUnquotedRune reports whether any rune satisfying match appears in s outside
// single/double quotes and not backslash-escaped.
func hasUnquotedRune(s string, match func(rune) bool) bool {
	runes := []rune(s)
	var quote rune
	esc := false
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case esc:
			esc = false
		case quote == '\'':
			// single quotes are literal; backslash does not escape inside them
			if r == '\'' {
				quote = 0
			}
		case r == '\\':
			esc = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case match(r):
			return true
		}
	}
	return false
}
