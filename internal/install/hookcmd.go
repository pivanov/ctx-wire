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

// powershellHookAgents are the agents CONFIRMED to execute hook commands through
// powershell.exe on Windows, which changes how a quoted path must be written.
// Only add an agent here with evidence (its own error log naming the shell), not
// by assumption: the call operator this enables is a command separator in cmd.
var powershellHookAgents = map[string]bool{
	"copilot": true,
}

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
// It is a var, not a plain func, so a test can pin another platform's form and
// drive the real installers with it. Without that seam every Windows branch is
// unreachable on a Unix test host and the assertions pass vacuously, which is how
// four Windows-only defects reached users.
var hookCommand = func(agent string) string {
	exe, err := os.Executable()
	if err != nil {
		// Nothing better available: hookCommandFor falls back to the
		// PATH-dependent form rather than write a broken absolute path.
		exe = ""
	} else if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return hookCommandFor(runtime.GOOS, exe, agent)
}

// hookCommandFor is the pure decision behind hookCommand, split out so a Unix
// test run can exercise the Windows branches. That split is not cosmetic: on a
// Unix host every Windows branch below is unreachable, so assertions about them
// either skip or pass vacuously, which is how four Windows-only defects reached
// users. Taking goos and exe as arguments makes each branch testable anywhere.
func hookCommandFor(goos, exe, agent string) string {
	suffix := " hook " + agent
	if goos != "windows" || exe == "" {
		return hookExeName + suffix
	}
	// A path with no spaces needs no quoting and runs as-is under both cmd and
	// PowerShell, so leave the common case exactly as it is.
	if !strings.ContainsAny(exe, " \t") {
		return exe + suffix
	}
	// Spaces DO need quoting (%LOCALAPPDATA% carries the user's display name, so
	// "C:\Users\User Name\..." is ordinary). But quoting alone is not enough for
	// PowerShell: a command that starts with a quote parses as a STRING
	// EXPRESSION, not an invocation, so `"C:\p ath\ctx-wire.exe" hook copilot`
	// silently does nothing. It needs the call operator.
	//
	// Copilot is known to run hooks through powershell.exe: its own log reports
	// `spawn powershell.exe EACCES` when the spawn is blocked (2026-08-16). For
	// the agents whose shell we have not confirmed, keep plain quoting rather
	// than guess, since `&` is a command separator in cmd.exe and would break it.
	if powershellHookAgents[agent] {
		return `& "` + exe + `"` + suffix
	}
	return `"` + exe + `"` + suffix
}

// HookNeedle is the substring doctor and the shim advisory grep config files for
// to decide whether an agent is wired. It deliberately OMITS the binary name so
// it matches both the bare and absolute-path forms; matching on
// "ctx-wire hook claude" would report a correctly-wired Windows install as
// unwired. Exported so out-of-package probes cannot drift back to the literal.
func HookNeedle(agent string) string { return "hook " + agent }

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

// migrateHookCommand rewrites entry["command"] to the form this build writes,
// when the value already there is ctx-wire's hook for agent in some OTHER form.
// It reports whether it rewrote anything.
//
// Every installer checks "is our hook already here?" with isHookCommand, which
// deliberately accepts all forms so an upgrade does not duplicate the entry.
// Without this, that same tolerance silently blocks the upgrade: a config wired
// before 0.1.66 holds the bare, PATH-dependent `ctx-wire hook <agent>`, init
// reports "already configured", and no reinstall of the binary can ever fix it.
// On Windows that leaves a hook the agent cannot spawn, which for Copilot denies
// tool calls outright. Recognizing an old form and canonicalizing it is the
// difference between shipping a fix and shipping a fix nobody receives.
func migrateHookCommand(entry map[string]any, agent string) bool {
	cur, _ := entry["command"].(string)
	if !isHookCommand(cur, agent) {
		return false
	}
	want := hookCommand(agent)
	if cur == want {
		return false
	}
	entry["command"] = want
	return true
}

// migrateNestedHookCommands canonicalizes every ctx-wire command inside a
// Claude/Codex-shaped entry, whose commands live in a nested "hooks" array.
func migrateNestedHookCommands(entry map[string]any, agent string) bool {
	inner, _ := entry["hooks"].([]any)
	changed := false
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if migrateHookCommand(hm, agent) {
			changed = true
		}
	}
	return changed
}
