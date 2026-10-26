package main

import (
	"fmt"
	"os"
	"strings"

	"ctx-wire/internal/hook"
	"ctx-wire/internal/rewrite"
)

// cmdHook runs as an agent pre-tool hook. It always exits 0 so it can never
// block the agent; a malformed payload is a silent passthrough.
func cmdHook(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire hook <agent>"},
			summary: "Agent pre-tool hook: read a tool-call payload on stdin, emit the rewritten command.",
			notes: []string{
				"Wired up for you by `ctx-wire init <agent>`. agent is one of: claude, codex, cursor, gemini, copilot.",
			},
		})
		return 0
	}
	if len(args) == 0 {
		usageLine(os.Stderr, "ctx-wire hook <agent>")
		return 2
	}
	switch args[0] {
	case "claude":
		_ = hook.Claude(os.Stdin, os.Stdout)
	case "cursor":
		_ = hook.Cursor(os.Stdin, os.Stdout)
	case "codex":
		_ = hook.Codex(os.Stdin, os.Stdout)
	case "gemini":
		_ = hook.Gemini(os.Stdin, os.Stdout)
	case "copilot":
		_ = hook.Copilot(os.Stdin, os.Stdout)
	default:
		// Unknown agent: passthrough rather than block.
	}
	return 0
}

// cmdRewrite prints the rewritten form of a shell command line.
//
// --shell selects the recognizer: "bash" (POSIX, the default) or
// "powershell". PowerShell lines are NOT POSIX lines: cmdlets/functions are
// not executables and the shell's default aliases shadow real exes, so only
// the powershell recognizer (PowerShellLineForAgent) may wrap them.
func cmdRewrite(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire rewrite [--agent <agent>] [--shell <bash|powershell>] <command line>"},
			summary: "Print how the hook would rewrite a shell command line (a debugging aid).",
			examples: []string{
				`ctx-wire rewrite "git status && ls -la | head"`,
				`ctx-wire rewrite --agent opencode "git status"`,
				`ctx-wire rewrite --shell powershell "git status"`,
				`ctx-wire rewrite --shell powershell --agent pi "curl -s https://example.com"`, // stays unwrapped (alias)
			},
			notes: []string{
				"Shows the rewrite only; it runs nothing. For the filter/mode decision, use `ctx-wire explain`.",
				"`--shell powershell` skips cmdlets and PowerShell default aliases; wrap only simple external commands.",
			},
		})
		return 0
	}

	agentName := ""
	shell := "bash"
	// Flags may appear in any leading order (--agent, --shell, and their = forms).
	for len(args) > 0 && (args[0] == "--agent" || args[0] == "--shell" ||
		strings.HasPrefix(args[0], "--agent=") || strings.HasPrefix(args[0], "--shell=")) {
		switch {
		case args[0] == "--agent":
			if len(args) < 2 {
				usageLine(os.Stderr, "ctx-wire rewrite --agent <agent> <command line>")
				return 2
			}
			agentName, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "--agent="):
			agentName = strings.TrimPrefix(args[0], "--agent=")
			if agentName == "" {
				usageLine(os.Stderr, "ctx-wire rewrite --agent <agent> <command line>")
				return 2
			}
			args = args[1:]
		case args[0] == "--shell":
			if len(args) < 2 {
				usageLine(os.Stderr, "ctx-wire rewrite --shell <bash|powershell> <command line>")
				return 2
			}
			shell, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "--shell="):
			shell = strings.TrimPrefix(args[0], "--shell=")
			if shell == "" {
				usageLine(os.Stderr, "ctx-wire rewrite --shell <bash|powershell> <command line>")
				return 2
			}
			args = args[1:]
		}
	}
	if len(args) == 0 {
		usageLine(os.Stderr, "ctx-wire rewrite [--shell <shell>] <command line>")
		return 2
	}
	// An unknown shell must not fall back to bash: the bash recognizer wraps
	// `ls` or `curl`, which PowerShell resolves to its own aliases.
	powershell := false
	switch strings.ToLower(shell) {
	case "bash":
	case "powershell", "pwsh":
		powershell = true
	default:
		fmt.Fprintf(os.Stderr, "ctx-wire rewrite: unknown shell %q (want bash or powershell)\n", shell)
		return 2
	}

	line := strings.Join(args, " ")
	if powershell {
		fmt.Println(rewrite.PowerShellLineForAgent(line, agentName))
		return 0
	}
	if agentName != "" {
		fmt.Println(rewrite.LineForAgent(line, agentName))
		return 0
	}
	fmt.Println(rewrite.Line(line))
	return 0
}
