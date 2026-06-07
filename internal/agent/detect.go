package agent

import (
	"os"
	"strings"
)

// procInfo is one process in the ancestor walk.
type procInfo struct {
	ppid int
	cmd  string
}

// detectMaxDepth bounds the ancestor walk so a pathological tree can never spin.
// Kept in sync with the shim's own ancestor-walk depth (internal/shim/shim.go)
// so attribution and activation agree on how far up the tree an agent counts.
const detectMaxDepth = 12

// detectFrom walks the process-ancestor chain starting at startPid, returning
// the first recognized agent (closest ancestor wins, so codex run inside an
// editor attributes to codex, not the editor). It is the pure core of detect,
// kept separate so it can be tested with a synthetic process map.
func detectFrom(startPid int, procs map[int]procInfo) string {
	pid := startPid
	for depth := 0; depth < detectMaxDepth && pid > 1; depth++ {
		p, ok := procs[pid]
		if !ok {
			return ""
		}
		if name := matchAgent(p.cmd); name != "" {
			return name
		}
		pid = p.ppid
	}
	return ""
}

type detectPattern struct {
	name     string
	patterns []string
}

var detectPatterns = []detectPattern{
	{name: "claude", patterns: []string{"claude"}},
	{name: "codex", patterns: []string{"codex"}},
	{name: "cursor", patterns: []string{"cursor"}},
	{name: "gemini", patterns: []string{"gemini"}},
	{name: "copilot", patterns: []string{"copilot"}},
	{name: "windsurf", patterns: []string{"windsurf"}},
	{name: "cline", patterns: []string{"cline"}},
	{name: "kilocode", patterns: []string{"kilocode"}},
	{name: "antigravity", patterns: []string{"antigravity"}},
	{name: "opencode", patterns: []string{"opencode"}},
	{name: "pi", patterns: []string{"pi-coding-agent", "pi coding agent", "/.pi/agent"}},
	{name: "hermes", patterns: []string{"hermes"}},
	{name: "vscode", patterns: []string{"vscode", "visual studio code"}},
	{name: "visualstudio", patterns: []string{"visualstudio", "visual studio"}},
}

// wireOnlyPatterns name ancestors that should route a command through ctx-wire
// but are not themselves attribution agents: the shim wires under them without
// forcing a CTX_WIRE_AGENT. agent-browser is the only one today. Attribution
// then falls to the runner's own Current() detection, so a command driven by a
// real agent above agent-browser (e.g. claude driving browser automation) is
// still attributed to that agent, matching the Unix shell shim; a standalone
// agent-browser command stays unattributed.
var wireOnlyPatterns = []string{"agent-browser"}

// scanTokens lowercases a process command and drops flag tokens, so a flag that
// merely contains an agent name (e.g. `gnome-terminal --cursor-shape`,
// `code --gemini`) does not self-attribute. The trailing space lets callers do a
// substring match without a word-boundary regex.
func scanTokens(cmd string) string {
	var b strings.Builder
	for _, f := range strings.Fields(strings.ToLower(cmd)) {
		if strings.HasPrefix(f, "-") {
			continue
		}
		b.WriteString(f)
		b.WriteByte(' ')
	}
	return b.String()
}

// matchAgent returns the canonical agent name whose token appears in a process
// command, or "" for none. The list is ordered, so the first matching agent wins
// on the rare command that mentions two.
//
// Bare-word collisions (`gcloud ai gemini`) remain inherently ambiguous; the
// explicit CTX_WIRE_AGENT marker set by hooks/shims is the authoritative signal,
// and this process-tree match is only the fallback.
func matchAgent(cmd string) string {
	scan := scanTokens(cmd)
	for _, item := range detectPatterns {
		for _, pattern := range item.patterns {
			if strings.Contains(scan, pattern) {
				return item.name
			}
		}
	}
	return ""
}

// matchWire is matchAgent plus the wire-only set. It returns whether a command
// should be routed through ctx-wire and, when it is an attribution agent, that
// agent's name (empty for a wire-only ancestor like agent-browser). This is the
// shim's activation rule; matchAgent stays attribution-only.
func matchWire(cmd string) (agent string, wire bool) {
	if name := matchAgent(cmd); name != "" {
		return name, true
	}
	scan := scanTokens(cmd)
	for _, p := range wireOnlyPatterns {
		if strings.Contains(scan, p) {
			return "", true
		}
	}
	return "", false
}

// detectShimFrom walks the ancestor chain like detectFrom but applies the shim
// activation rule (matchWire), returning whether to wire and which agent to
// attribute. Closest ancestor wins. Pure, for testing with a synthetic map.
func detectShimFrom(startPid int, procs map[int]procInfo) (wire bool, agent string) {
	pid := startPid
	for depth := 0; depth < detectMaxDepth && pid > 1; depth++ {
		p, ok := procs[pid]
		if !ok {
			return false, ""
		}
		if name, w := matchWire(p.cmd); w {
			return true, name
		}
		pid = p.ppid
	}
	return false, ""
}

// DetectShim reports whether the current process is running under an agent (so a
// shim should route its command through ctx-wire) and the agent to attribute it
// to (empty for a wire-only ancestor). It walks one process snapshot. Used by
// `ctx-wire run --shim`; callers should consult CTX_WIRE_AGENT and the opt-out
// envs first so this walk only runs when the environment does not decide.
func DetectShim() (wire bool, agent string) {
	if os.Getenv(envDetect) == "0" {
		return false, ""
	}
	procs := procSnapshot()
	if len(procs) == 0 {
		return false, ""
	}
	return detectShimFrom(os.Getppid(), procs)
}
