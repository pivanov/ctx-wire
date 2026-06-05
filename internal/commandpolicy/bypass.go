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
	"less": true, "more": true, "top": true, "htop": true, "man": true,
	"ssh": true, "tmux": true, "screen": true, "fzf": true,
	"gdb": true, "lldb": true, "mysql": true, "psql": true, "redis-cli": true,
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
		return true, "interactive program " + base
	}
	for _, a := range args {
		if IsStreamingArg(a) {
			return true, "streaming flag " + a
		}
	}
	return false, ""
}
