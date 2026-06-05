//go:build windows

package shim

import (
	"os"
	"strings"
)

// isExecutable reports whether a stat'd file may be run. On Windows executability
// is decided by the file extension (handled in executableExts), not mode bits:
// os.Stat returns 0666 for every regular file, so a permission check would
// reject every real binary and shim. Any non-directory file that matched a
// runnable extension is therefore executable.
func isExecutable(info os.FileInfo) bool {
	return !info.IsDir()
}

// executableExts are the runnable filename suffixes, taken from PATHEXT (the
// same set the OS and exec.LookPath use). Used to resolve a bare command to a
// real binary: the ctx-wire shim is "git.cmd", but the real git is "git.exe",
// so resolution must try every PATHEXT extension, not just the shim's.
func executableExts() []string {
	raw := os.Getenv("PATHEXT")
	if raw == "" {
		raw = ".COM;.EXE;.BAT;.CMD"
	}
	var exts []string
	for _, e := range strings.Split(raw, ";") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		exts = append(exts, strings.ToLower(e))
	}
	if len(exts) == 0 {
		exts = []string{".exe"}
	}
	return exts
}
