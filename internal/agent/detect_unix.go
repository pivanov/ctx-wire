//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// detect walks this process's ancestor chain looking for a known agent. It takes
// a single `ps` snapshot and walks it in memory, so the cost is one exec per
// call (and only on the hook path: shim-launched commands already carry
// CTX_WIRE_AGENT). Best-effort: "" when ps fails or no agent is found.
func detect() string {
	procs := psSnapshot()
	if len(procs) == 0 {
		return ""
	}
	return detectFrom(os.Getppid(), procs)
}

// psSnapshot returns a pid -> {ppid, command} map from one `ps` call. Works on
// macOS and Linux. A parse failure for one line is skipped, not fatal.
func psSnapshot() map[int]procInfo {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil
	}
	procs := map[int]procInfo{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs[pid] = procInfo{ppid: ppid, cmd: strings.Join(fields[2:], " ")}
	}
	return procs
}
