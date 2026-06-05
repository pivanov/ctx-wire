package agent

import "strings"

// procInfo is one process in the ancestor walk.
type procInfo struct {
	ppid int
	cmd  string
}

// detectMaxDepth bounds the ancestor walk so a pathological tree can never spin.
const detectMaxDepth = 16

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

// matchAgent returns the canonical agent name whose token appears in a process
// command, or "" for none. Known is ordered, so the first listed agent wins on
// the rare command that mentions two.
func matchAgent(cmd string) string {
	low := strings.ToLower(cmd)
	for _, name := range Known {
		if strings.Contains(low, name) {
			return name
		}
	}
	return ""
}
