package install

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// hookExeName is ctx-wire's own binary name, without any platform suffix.
const hookExeName = "ctx-wire"

// hookCommand returns the command string ctx-wire writes into an agent's hook
// config.
//
// On Unix it is the bare name, resolved through PATH like any other command.
//
// On WINDOWS it is the absolute path to ctx-wire.exe, because a bare name there
// is not reliable: the installer appends %LOCALAPPDATA%\ctx-wire\bin to the user
// PATH and broadcasts WM_SETTINGCHANGE, but already-running processes keep the
// environment they started with. An agent launched before (or outside) that
// update cannot resolve `ctx-wire`, so spawning the hook fails.
//
// That failure is far worse than it sounds. The hook body is carefully
// fail-open (every error path returns "no opinion"), but that only protects a
// hook that RUNS. A hook that cannot be spawned fails closed in the agent, and
// at least Copilot turns "hook errored" into a denial of EVERY tool call,
// including ones ctx-wire never touches. Reported 2026-08-16: a Windows user
// had `List directory` and skills denied, with nothing wrong on their machine
// but PATH visibility. An absolute path removes the dependency entirely.
func hookCommand(agent string) string {
	suffix := " hook " + agent
	if runtime.GOOS != "windows" {
		return hookExeName + suffix
	}
	exe, err := os.Executable()
	if err != nil {
		// Nothing better available: fall back to the PATH-dependent form rather
		// than write a broken absolute path.
		return hookExeName + suffix
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	// Windows install paths routinely contain spaces (a user's display name ends
	// up in %LOCALAPPDATA%), and the agent splits this string on whitespace.
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"`
	}
	return exe + suffix
}

// hookNeedle is the substring doctor greps config files for to decide whether an
// agent is wired. It deliberately OMITS the binary name so it matches both the
// bare and absolute-path forms; matching on "ctx-wire hook claude" would report
// a correctly-wired Windows install as unwired.
func hookNeedle(agent string) string { return "hook " + agent }

// isHookCommand reports whether cmd is one of ctx-wire's hook entries for agent,
// in EITHER form: the bare `ctx-wire hook <agent>` written on Unix and by older
// versions, or the absolute `C:\...\ctx-wire.exe hook <agent>` written on
// Windows. Detection and uninstall must accept both, or upgrading a Windows
// install would orphan the old entry and leave a duplicate behind.
func isHookCommand(cmd, agent string) bool {
	cmd = strings.TrimSpace(cmd)
	suffix := "hook " + agent
	if !strings.HasSuffix(cmd, suffix) {
		return false
	}
	head := strings.TrimSpace(strings.TrimSuffix(cmd, suffix))
	head = strings.Trim(head, `"`)
	head = strings.TrimSpace(head)
	if head == "" {
		return false
	}
	if head == hookExeName {
		return true
	}
	// Compare on the base name so any install directory matches. Normalize
	// separators first: a config written on Windows must still be recognized by
	// path.Base, which only understands forward slashes.
	base := strings.ToLower(path.Base(strings.ReplaceAll(head, `\`, "/")))
	return base == hookExeName || base == hookExeName+".exe"
}

// cmdMatcher decides whether a hook entry's command belongs to ctx-wire.
// Removal needs two flavors: most agents get a `ctx-wire hook <agent>` command
// (which now has two forms, bare and absolute), while Gemini gets the PATH of a
// wrapper script ctx-wire wrote. Matching those with one string comparison is
// what made the absolute-path change silently break Gemini uninstall.
type cmdMatcher func(string) bool

// agentHook matches either form of ctx-wire's hook command for agent.
func agentHook(agent string) cmdMatcher {
	return func(cmd string) bool { return isHookCommand(cmd, agent) }
}

// exactCommand matches one literal command, for entries that are not a
// `ctx-wire hook <agent>` invocation at all.
func exactCommand(want string) cmdMatcher {
	return func(cmd string) bool { return cmd == want }
}
