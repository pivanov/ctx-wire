package runner

import (
	"os"
	"runtime"

	"ctx-wire/internal/rewrite"
	"ctx-wire/internal/shim"
)

// shellSplitArgs returns args for `<shell> -c <script>` with each segment of the
// script wrapped in this ctx-wire binary, or ok=false to run the command as-is.
// The wrap uses this executable's absolute path: a login shell (-l) may rebuild
// PATH (Debian's /etc/profile does), and a wrapped segment that cannot find
// ctx-wire would break a command that worked. Windows shells and PATH-shim
// traffic are left alone.
func shellSplitArgs(name string, args []string) ([]string, bool) {
	if runtime.GOOS == "windows" || os.Getenv(shim.EnvName) != "" {
		return nil, false
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return nil, false
	}
	return rewrite.ShellScriptArgs(name, args, rewrite.ShellSingleQuote(exe)+" run ")
}

// stdoutIsFile reports whether stdout is redirected to a regular file, where
// the reader is whatever parses that file later, never the agent. A var so tests
// are not affected by how the test binary's own stdout is attached.
var stdoutIsFile = func() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode().IsRegular()
}
