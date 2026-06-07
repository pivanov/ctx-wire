//go:build windows

package agent

import (
	"os"
	"syscall"
	"unsafe"
)

// detect walks this process's ancestor chain looking for a known agent, using a
// single Toolhelp process snapshot. Best-effort: "" when the snapshot fails or
// no agent is found.
//
// Note: a Toolhelp snapshot exposes each process's image name (claude.exe), not
// its full command line. CLI agents whose identity is in their exe name match;
// editor hosts (code.exe, devenv.exe) do not, so they attribute via an explicit
// CTX_WIRE_AGENT set by their MCP server or hook, which Current() already
// prefers. This is the intended boundary on Windows.
func detect() string {
	procs := procSnapshot()
	if len(procs) == 0 {
		return ""
	}
	return detectFrom(os.Getppid(), procs)
}

// procSnapshot returns a pid -> {ppid, image name} map from one Toolhelp
// snapshot. Best-effort: nil when the snapshot cannot be taken; a bad entry is
// skipped, never fatal.
func procSnapshot() map[int]procInfo {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(snap)

	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	procs := map[int]procInfo{}
	for err := syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		procs[int(e.ProcessID)] = procInfo{
			ppid: int(e.ParentProcessID),
			cmd:  syscall.UTF16ToString(e.ExeFile[:]),
		}
	}
	return procs
}
