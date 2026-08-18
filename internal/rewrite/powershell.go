package rewrite

import (
	"strings"

	"ctx-wire/internal/agent"
)

// PowerShell support exists because Copilot CLI drives PowerShell on Windows: it
// sends toolName "powershell", not "bash". The hook understood only bash, so on
// Windows ctx-wire saw every command and wrapped none of them.
//
// A PowerShell line is NOT a POSIX line, so it does not go through Line. Two
// differences make the bash recognizer unsafe here:
//
//   - Most PowerShell commands are cmdlets (Get-ChildItem) or functions, not
//     executables. `ctx-wire run Get-ChildItem` would exec a program that does
//     not exist and turn a working command into "command not found".
//   - PowerShell's default aliases SHADOW real executables. `curl` means
//     Invoke-WebRequest while curl.exe also exists in System32; `where` means
//     Where-Object while where.exe exists; `sort`, `tee`, `diff`, `ls` and `cat`
//     shadow Git-for-Windows tools. Resolving those on PATH would silently run a
//     different program with incompatible arguments.
//
// So this path is deliberately narrower than the bash one: exactly one simple
// command, no pipelines, no operators, first token an external executable that
// is not a shadowing alias. Everything else passes through. Missing a wrap costs
// tokens; a bad wrap breaks the user's command, and ctx-wire observes commands,
// it must never break them.

// powerShellOperators are characters that introduce PowerShell syntax this
// recognizer does not model: pipelines and object flow (|), statement separators
// (;), the call/background operator and && || chains (&), redirection (< >),
// variable and subexpression expansion ($ ` @), and grouping ( ) { }.
//
// The check is intentionally applied to the WHOLE line, quoted regions included.
// That over-rejects (a commit message containing parentheses passes through
// unwrapped), which costs a little coverage and cannot corrupt a command. A
// quote-aware scanner is the obvious upgrade once this has real mileage.
const powerShellOperators = "|;&<>$`@(){}"

// powerShellAliases are PowerShell's built-in aliases that shadow a real
// executable of the same name on a typical Windows dev machine. Wrapping one
// would run the executable instead of the cmdlet, with arguments meant for the
// cmdlet. Sourced from PowerShell's default alias table, filtered to the names
// that actually collide.
var powerShellAliases = map[string]bool{
	// Get-ChildItem / Get-Content / Get-Location
	"ls": true, "dir": true, "gci": true, "cat": true, "type": true, "gc": true,
	"pwd": true, "gl": true,
	// Item manipulation
	"cp": true, "copy": true, "cpi": true, "mv": true, "move": true, "mi": true,
	"rm": true, "del": true, "erase": true, "rd": true, "ri": true, "rmdir": true,
	"md": true, "ni": true, "sc": true, "si": true,
	// Process and system
	"ps": true, "kill": true, "sleep": true, "mount": true, "man": true,
	"clear": true, "cls": true, "start": true, "saps": true, "spps": true,
	// Object pipeline verbs that collide with coreutils
	"sort": true, "tee": true, "diff": true, "select": true, "where": true,
	"group": true, "measure": true, "compare": true, "foreach": true,
	// Web
	"curl": true, "wget": true, "iwr": true,
	// Output and history
	"echo": true, "write": true, "history": true, "h": true, "set": true,
	"fc": true, "ft": true, "fl": true,
}

// PowerShellLineForAgent rewrites a PowerShell command line so a wrappable
// command routes through `ctx-wire run`, attributed to agentName. It returns the
// input unchanged when nothing can be wrapped safely.
func PowerShellLineForAgent(line, agentName string) string {
	wrap := prefix
	if name := agent.Normalize(agentName); name != "" {
		wrap = "ctx-wire run --agent " + name + " "
	}
	return powerShellLineWith(line, wrap)
}

func powerShellLineWith(line, wrap string) string {
	core := strings.TrimSpace(line)
	if core == "" {
		return line
	}
	// A command the model already typed as `ctx-wire run ...` (which the agent
	// instructions teach) is wrapped but unattributed. Stamp the agent in, the
	// same as the bash path, so those savings are not recorded as (unattributed).
	if stamped, _, ok := stampWrappedAgent(core, wrap); ok {
		return stamped
	}
	if strings.ContainsAny(core, powerShellOperators) {
		return line
	}
	// passReason is the shared shape classifier: already-ctx-wire, redirection,
	// dynamic command tokens, shell builtins and keywords, excluded commands and
	// env-assignment prefixes. Reusing it keeps the two shells from drifting.
	if passReason(core) != "" {
		return line
	}
	first := firstToken(core)
	if powerShellAliases[strings.ToLower(first)] {
		return line
	}
	// Same runtime gate as rewriteSegment: only wrap something that actually
	// resolves to an executable. This is what keeps cmdlets and PowerShell
	// functions from being wrapped, since neither is on PATH.
	if !lookPath(first) {
		return line
	}
	return wrap + core
}
