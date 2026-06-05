// Package agent owns the identity of the AI coding agent that invoked ctx-wire.
// A hook (which knows its agent from its subcommand) or a shim (which detects
// the agent by walking the process tree) sets CTX_WIRE_AGENT; `ctx-wire run`
// reads it and records it on each gain entry so savings can be attributed and
// broken down per agent. The value is always one ctx-wire itself produces, but
// it is normalized defensively (lowercase, restricted charset) so a stray
// environment value can never corrupt the log or a shell assignment.
package agent

import (
	"os"
	"strings"
)

// EnvName is the environment variable that carries the invoking agent's name.
const EnvName = "CTX_WIRE_AGENT"

// envDetect gates process-tree agent detection. Set it to "0" to turn detection
// off (no `ps` call, no attribution beyond an explicit CTX_WIRE_AGENT). Tests
// use it for determinism; users can use it to opt out.
const envDetect = "CTX_WIRE_AGENT_DETECT"

// maxNameLen bounds a recorded agent name. Real names are short ("claude",
// "copilot"); the cap is defense against a pathological environment value.
const maxNameLen = 32

// Known lists the agent names ctx-wire produces today. It is informational
// (used by tests and docs); Normalize accepts any well-formed name, not just
// these, so a new agent does not require a code change here.
var Known = []string{"claude", "codex", "cursor", "gemini", "copilot", "windsurf", "cline"}

// Normalize lowercases and validates an agent name. It returns "" for an empty,
// over-long, or otherwise malformed value (anything outside [a-z0-9-]). The
// restricted charset keeps the name safe to splice into a shell assignment
// without quoting and to use as an aggregation key.
func Normalize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > maxNameLen {
		return ""
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			return ""
		}
	}
	return name
}

// Current returns the name of the agent that invoked this process, or "" when
// unattributed. An explicit CTX_WIRE_AGENT wins (set by a shim's export, an
// agent's env config, or a test); otherwise it falls back to walking the
// process tree, so a hook-rewritten `ctx-wire run` is attributed without the
// hook having to bake the agent into the visible command line.
func Current() string {
	if a := Normalize(os.Getenv(EnvName)); a != "" {
		return a
	}
	if os.Getenv(envDetect) == "0" {
		return ""
	}
	return detect()
}
