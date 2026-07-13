// Package commandpolicy contains shared command-shape decisions used by runtime
// execution and diagnostics.
package commandpolicy

import (
	"path/filepath"
	"strings"
)

// InteractivePrograms are commands that require a live terminal; capturing their
// output would hang or corrupt the session.
var InteractivePrograms = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "nano": true, "emacs": true,
	"less": true, "more": true, "top": true, "htop": true, "watch": true, "man": true,
	"ssh": true, "tmux": true, "screen": true, "fzf": true,
	"gdb": true, "lldb": true, "mysql": true, "mariadb": true,
	"psql": true, "redis-cli": true,
	"python": true, "python3": true, "node": true, "irb": true, "ipython": true,
}

// mcpLaunchers are interpreters and package runners that commonly start a
// long-lived MCP stdio server from a script or package. node/python are already
// covered by InteractivePrograms; the rest (bun, deno, npx, uvx, ...) are why
// MCP servers launched through them were captured and deadlocked.
var mcpLaunchers = map[string]bool{
	"node": true, "bun": true, "deno": true, "tsx": true, "ts-node": true,
	"npx": true, "bunx": true, "pnpm": true, "pnpx": true, "yarn": true,
	"python": true, "python3": true, "uv": true, "uvx": true, "pipx": true,
	"php": true, "ruby": true, "dotnet": true,
}

// devScriptRunners are package managers whose `run <script>` (or shorthand)
// commonly starts a long-lived dev server or watcher from a package.json script.
var devScriptRunners = map[string]bool{
	"npm": true, "pnpm": true, "yarn": true, "bun": true,
}

// excludedCommands are command basenames the user opted out of via the config
// file ([hooks] exclude_commands). Such commands are never rewritten by the
// hook and never filtered by the runner. Set once at startup; read-only after.
var excludedCommands = map[string]bool{}

// SetExcludedCommands records the user's exclude_commands list (matched by
// basename). Empty/blank entries are ignored.
func SetExcludedCommands(cmds []string) {
	m := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		c = strings.TrimSpace(c)
		if c != "" {
			m[c] = true
		}
	}
	excludedCommands = m
}

// IsExcluded reports whether name (matched by basename) was opted out via config.
func IsExcluded(name string) bool {
	return excludedCommands[filepath.Base(name)]
}

// fullReadCommands are commands that emit a file's entire contents. For these, a
// path argument matching a full-file pattern bypasses output capping so an
// instruction or skill file reaches the agent whole (still scrubbed). head/tail
// are excluded on purpose: they are partial reads by intent.
var fullReadCommands = map[string]bool{"cat": true, "nl": true, "bat": true}

// defaultFullFilePatterns protect agent-instruction files that must be read
// whole. Skill files are named SKILL.md by convention (including under a
// skills/<name>/ directory), so a basename match covers them anywhere.
var defaultFullFilePatterns = []string{"SKILL.md", "AGENTS.md", "CLAUDE.md"}

// fullFilePatterns is the active set: the defaults plus any configured globs.
var fullFilePatterns = defaultFullFilePatterns

// SetFullFiles extends the built-in full-file patterns with the user globs from
// [hooks] full_files. The defaults always apply; user patterns are added, not
// replaced. Blank entries are ignored.
func SetFullFiles(patterns []string) {
	out := append([]string{}, defaultFullFilePatterns...)
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	fullFilePatterns = out
}

// IsFullFileRead reports whether name+args is a whole-file read of files that
// must not be capped (instruction or skill files). True only when the command is
// a full-content reader (cat/nl/bat) and EVERY file operand matches a full-file
// pattern (with at least one operand). The all-operands rule is the guard against
// a context flood: `cat SKILL.md huge.log` must NOT uncap, because cat emits one
// concatenated stream and we cannot keep only the skill half. Read the skill file
// on its own to get it whole.
func IsFullFileRead(name string, args []string) bool {
	if !fullReadCommands[filepath.Base(name)] {
		return false
	}
	operands := 0
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue // a flag (or the `--` separator), not a file operand
		}
		operands++
		if !matchesFullFilePattern(a) {
			return false // a non-full-file operand would be flooded too: do not uncap
		}
	}
	return operands > 0
}

// IsMachineReadable reports whether name+args request machine-parseable output
// that a tool, not the agent, consumes: git's --porcelain, git -z, or a git
// --format/--pretty template. Such output must be streamed whole and unfiltered,
// because a line filter or dedup would corrupt what an IDE's Source Control or a
// script parses, e.g. VS Code polling `git status --porcelain -z` and getting a
// truncated file list.
//
// Scope is deliberately git-only (plus --porcelain, which only VCS tools use):
// -z means gzip for tar/xz, and a bare -0/--null is a coincidental argument for
// python/kill/etc. Generic NUL-separated idioms (find -print0, sort -z, grep -Z,
// xargs -0) share this bug class and are a separate, per-tool follow-up.
func IsMachineReadable(name string, args []string) bool {
	git := filepath.Base(name) == "git"
	for _, a := range args {
		switch {
		case a == "--porcelain" || strings.HasPrefix(a, "--porcelain="):
			return true
		case git && a == "-z":
			return true
		case git && (a == "--format" || strings.HasPrefix(a, "--format=") ||
			strings.HasPrefix(a, "--pretty=format:") || strings.HasPrefix(a, "--pretty=tformat:")):
			// git log/for-each-ref/branch/tag --format is a custom template a tool
			// parses (VS Code's history graph); never a filter target.
			return true
		}
	}
	return false
}

// matchesFullFilePattern reports whether path's basename matches any active
// full-file pattern, using filepath.Match glob syntax (*, ?, [..]).
func matchesFullFilePattern(path string) bool {
	base := filepath.Base(path)
	for _, pat := range fullFilePatterns {
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
	}
	return false
}

// transparentPrefixes are wrapper command prefixes (from config) that the hook
// peels before routing the inner command. Set once at startup; read-only after.
var transparentPrefixes []string

// SetTransparentPrefixes records the configured wrapper prefixes (e.g.
// "docker exec web"). Blank entries are ignored.
func SetTransparentPrefixes(prefixes []string) {
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	transparentPrefixes = out
}

// TransparentPrefix reports whether core begins with a configured wrapper prefix
// followed by an inner command. On a match it returns the byte offset where the
// inner command starts (past the prefix and its trailing whitespace).
func TransparentPrefix(core string) (int, bool) {
	for _, p := range transparentPrefixes {
		if !strings.HasPrefix(core, p) {
			continue
		}
		rest := core[len(p):]
		if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue // not a whole-token boundary, or no inner command
		}
		i := len(p)
		for i < len(core) && (core[i] == ' ' || core[i] == '\t') {
			i++
		}
		if i >= len(core) {
			continue
		}
		return i, true
	}
	return 0, false
}

// IsInteractiveProgram reports whether name is a command that should bypass
// capture and run with inherited stdio.
func IsInteractiveProgram(name string) bool {
	return InteractivePrograms[filepath.Base(name)]
}

// isLongRunningScriptName reports whether a package.json script name
// conventionally starts a process that runs until interrupted (a dev server,
// watcher, or preview server). Both namespaced forms are matched: dev:api and
// css:watch as well as bare dev/watch.
func isLongRunningScriptName(s string) bool {
	s = strings.ToLower(s)
	head := s
	if i := strings.IndexByte(head, ':'); i >= 0 {
		head = head[:i]
	}
	switch head {
	case "dev", "start", "serve", "watch", "storybook", "preview":
		return true
	}
	for _, kw := range [...]string{"dev", "watch", "serve", "start", "storybook", "preview"} {
		if strings.HasSuffix(s, ":"+kw) {
			return true
		}
	}
	return false
}

// devScriptArg returns the script name a package-manager invocation will run,
// skipping the optional "run" verb and any leading flags (including the
// workspace/filter flags that take a value). It reports false when no script
// token is present.
func devScriptArg(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "run":
			continue
		case a == "--filter", a == "-F", a == "--workspace", a == "-w", a == "--dir", a == "-C":
			i++ // flag consumes the following value
		case strings.HasPrefix(a, "-"):
			continue
		default:
			return a, true
		}
	}
	return "", false
}

// IsLongRunningDevScript reports whether name+args is a package-manager
// invocation of a long-running dev/watch/serve script. Such a script never
// exits on its own, so capturing it buffers its output until the process ends
// and the agent sees nothing; it must run with inherited stdio and stream live.
// Ordinary scripts (build, test, lint) are unaffected and stay filtered.
func IsLongRunningDevScript(name string, args []string) bool {
	if !devScriptRunners[filepath.Base(name)] {
		return false
	}
	script, ok := devScriptArg(args)
	if !ok {
		return false
	}
	return isLongRunningScriptName(script)
}

// streamingFlagsByCommand lists, per command, the flags that mean "stream
// unbounded live output" and so must bypass capture. Bare -f/-F/-w collide
// with non-streaming flags (grep -F, ls -F, sort -f, git commit -F, pnpm -F),
// so streaming is recognized only for these (command, flag) pairs. The risk is
// asymmetric: omitting a real streaming pair makes ctx-wire capture an endless
// stream and hang, so this list errs toward inclusion.
var streamingFlagsByCommand = map[string][]string{
	"tail":       {"-f", "-F", "--follow", "--retry"},
	"kubectl":    {"-f", "--follow", "-w", "--watch"},
	"oc":         {"-f", "--follow", "-w", "--watch"},
	"docker":     {"-f", "--follow"},
	"podman":     {"-f", "--follow"},
	"journalctl": {"-f", "--follow"},
	"vagrant":    {"-f", "--follow"},
	"heroku":     {"-f", "--follow"},
	"pm2":        {"-f", "--follow"},
	"wrangler":   {"-f", "--follow"},
}

// IsStreamingArg reports whether an argument usually means unbounded live
// output, which should bypass capture.
func IsStreamingArg(arg string) bool {
	switch arg {
	case "-f", "--follow", "-F", "--watch", "-w":
		return true
	default:
		return false
	}
}

// isWordByte reports whether b continues an "mcp" token. Only letters and
// digits count: path and package separators (including '_' and '-') act as
// delimiters, so "fetch_mcp" and "mcp-server" match but "mcprc" does not.
func isWordByte(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

// hasMCPToken reports whether s carries an MCP marker: the well-known package
// namespace, or "mcp" as a token delimited by start/end or any non-identifier
// byte (so it matches "mcp-server-git", "@modelcontextprotocol/server-x",
// ".../packages/mcp/bin.ts", "fetch_mcp.py" but not "mcprc" or "compute").
func hasMCPToken(s string) bool {
	s = strings.ToLower(s)
	if strings.Contains(s, "modelcontextprotocol") {
		return true
	}
	// Scan by absolute index over the original string. (Re-slicing would drop the
	// byte before a match, so "xmcpmcp" would falsely read the second "mcp" as
	// starting at offset 0, i.e. token-delimited, and match.)
	for i := 0; i+3 <= len(s); i++ {
		if s[i:i+3] != "mcp" {
			continue
		}
		before := i == 0 || !isWordByte(s[i-1])
		after := i+3 == len(s) || !isWordByte(s[i+3])
		if before && after {
			return true
		}
	}
	return false
}

// IsMCPServer reports whether name+args look like the launch of an MCP stdio
// server. Such a server speaks JSON-RPC over stdin/stdout and runs until the
// client disconnects, so any buffering, filtering, or scrubbing of that stream
// deadlocks the handshake. It must run with inherited stdio.
//
// A launch qualifies when the program itself is MCP-named (a compiled server
// like "mcp-server-git"), or when a known script/package launcher carries an
// MCP token in its arguments (`bun .../mcp/bin.ts`, `npx @modelcontextprotocol/
// server-fs`, `uvx mcp-server-fetch`). The launcher gate keeps unrelated
// commands that merely mention an "mcp" path (`go test ./mcp/...`) out.
func IsMCPServer(name string, args []string) bool {
	base := filepath.Base(name)
	if hasMCPToken(base) {
		return true
	}
	if !mcpLaunchers[base] {
		return false
	}
	for _, a := range args {
		if hasMCPToken(a) {
			return true
		}
	}
	return false
}

// interpreters are the interpreter basenames whose invocations may be finite
// (one-shot) rather than interactive. When interpreterIsFinite returns true for
// the args, the command falls through to normal capture+scrub instead of
// inheriting stdio unchanged.
var interpreters = map[string]bool{
	"python": true, "python3": true, "node": true, "ipython": true, "irb": true,
}

// interpreterLongRunningModules are python -m <module> values that start a
// long-lived server; these must NOT be captured (they would block or deadlock).
var interpreterLongRunningModules = map[string]bool{
	"http.server": true, "https.server": true, "uvicorn": true, "gunicorn": true,
	"flask": true, "fastapi": true, "celery": true,
}

// interpreterIsFinite returns true when the interpreter invocation is a finite,
// non-interactive one-shot command (capture it). Bare invocations,
// interactive flags (-i), and known long-running -m modules are NOT finite
// (keep inherited stdio).
func interpreterIsFinite(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" || a == "-e" || a == "--eval":
			return true
		case a == "-m":
			// -m <module>: the module and its args follow; a known long-running
			// server module keeps inherited stdio, otherwise it is finite.
			if i+1 < len(args) && interpreterLongRunningModules[args[i+1]] {
				return false
			}
			return true
		case a == "-i":
			return false
		case strings.HasPrefix(a, "-"):
			continue // another interpreter flag (e.g. -O, -B): keep scanning
		default:
			// First positional is the script path; its own args follow and must
			// not be reinterpreted as interpreter flags (e.g. `python x.py -i` is
			// the script's flag, not Python's interactive mode).
			return true
		}
	}
	return false
}

// dbOneShotFlags maps an interactive database client to the flags that make it
// run a single query and exit. When present, the client's output is finite and
// safe to capture+scrub instead of bypassing (which would leak an unscrubbed
// query result: a token stored in a row, a connection string with a password).
var dbOneShotFlags = map[string][]string{
	"mysql":   {"-e", "--execute"},
	"mariadb": {"-e", "--execute"},
	"psql":    {"-c", "--command", "-l", "--list", "-f", "--file"},
}

// redisCLIValueFlags consume the following arg as their value, so that arg is
// not the command keyword. "-x" is deliberately excluded: it makes redis-cli
// read the final argument from stdin, which would block on a passthrough
// terminal, so a command using it stays bypassed.
var redisCLIValueFlags = map[string]bool{
	"-h": true, "-p": true, "-s": true, "-a": true, "-u": true, "-n": true,
	"-i": true, "-t": true, "--user": true, "--pass": true,
}

// redisBlockingCommands stream output or block indefinitely (MONITOR, the
// SUBSCRIBE family, blocking list/stream pops and WAIT that can take a 0 =
// infinite timeout). They carry a command keyword but never return on their
// own, so they must stay bypassed. Matched case-insensitively.
var redisBlockingCommands = map[string]bool{
	"monitor": true, "subscribe": true, "psubscribe": true, "ssubscribe": true,
	"blpop": true, "brpop": true, "blmove": true, "blmpop": true,
	"brpoplpush": true, "bzpopmin": true, "bzpopmax": true, "bzmpop": true,
	"xread": true, "xreadgroup": true, "wait": true, "waitaof": true,
}

// psqlFileReadsStdin reports whether a psql -f/--file argument points at stdin
// ("-") in any of its spellings (`-f -`, `--file -`, `--file=-`, `-f-`). Such a
// form reads SQL from stdin, which would block on the inherited terminal under
// capture, so it must stay bypassed even though -f is otherwise a one-shot flag.
func psqlFileReadsStdin(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-f-" || a == "--file=-":
			return true
		case a == "-f" || a == "--file":
			if i+1 < len(args) && args[i+1] == "-" {
				return true
			}
		}
	}
	return false
}

// dbCommandIsFinite reports whether an interactive db client is being run in a
// one-shot form that produces finite output (so it can be captured + scrubbed).
// Bare invocations (an interactive REPL) return false and stay bypassed.
func dbCommandIsFinite(base string, args []string) bool {
	if base == "redis-cli" {
		return redisHasCommand(args)
	}
	flags := dbOneShotFlags[base]
	if flags == nil {
		return false
	}
	if base == "psql" && psqlFileReadsStdin(args) {
		return false // `psql -f -` reads SQL from stdin: would block under capture
	}
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
			if strings.HasPrefix(f, "--") {
				if strings.HasPrefix(a, f+"=") {
					return true // --command=...
				}
			} else if len(a) > 2 && strings.HasPrefix(a, f) {
				return true // attached short-flag value: -e"SELECT 1"
			}
		}
	}
	return false
}

// redisHasCommand reports whether a redis-cli invocation carries a finite
// command keyword (`redis-cli GET foo`, `redis-cli -h host PING`) whose output
// is safe to capture. It returns false for an interactive REPL (no command), a
// form that reads stdin (-x, --pipe), repeat mode (-r, whose -1 repeats
// forever), and streaming/blocking commands (MONITOR, SUBSCRIBE, blocking pops).
func redisHasCommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-x" || a == "--pipe" || a == "--pipe-mode":
			return false // reads from stdin: would block on a passthrough terminal
		case a == "-r":
			return false // repeat mode; `-r -1` repeats forever
		case redisCLIValueFlags[a]:
			i++ // skip the flag's value
			continue
		case strings.HasPrefix(a, "-"):
			continue // another flag (e.g. --scan, --no-raw)
		default:
			// First positional is the command keyword: capture it unless it is a
			// streaming/blocking command that never returns on its own.
			return !redisBlockingCommands[strings.ToLower(a)]
		}
	}
	return false
}

// ClassifyBypass reports whether a command should bypass capture and, if so,
// returns a human-readable reason.
func ClassifyBypass(name string, args []string) (bool, string) {
	if IsExcluded(name) {
		return true, "excluded by config"
	}
	if IsMCPServer(name, args) {
		return true, "mcp stdio server"
	}
	if IsLongRunningDevScript(name, args) {
		return true, "long-running dev script"
	}
	if base := filepath.Base(name); InteractivePrograms[base] {
		switch {
		case interpreters[base] && interpreterIsFinite(args):
			// finite one-shot interpreter command -> fall through to capture+scrub
		case dbCommandIsFinite(base, args):
			// finite one-shot db query -> fall through to capture+scrub
		default:
			return true, "interactive program " + base
		}
	}
	if flags := streamingFlagsByCommand[filepath.Base(name)]; flags != nil {
		for _, a := range args {
			for _, sf := range flags {
				if a == sf {
					return true, "streaming flag " + a
				}
			}
		}
	}
	return false, ""
}
