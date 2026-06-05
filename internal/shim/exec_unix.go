//go:build !windows

package shim

import "os"

// isExecutable reports whether a stat'd file may be run. On Unix this is the
// owner/group/other execute bits.
func isExecutable(info os.FileInfo) bool {
	return info.Mode().Perm()&0o111 != 0
}

// executableExts are the filename suffixes a bare command may carry. Unix has
// none (the command name is the file name), so a single empty extension is
// tried.
func executableExts() []string {
	return []string{""}
}
