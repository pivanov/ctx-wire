package main

import (
	"encoding/json"
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
func cmdRewrite(args []string) int {
	const usage = "ctx-wire rewrite [--json] [--agent <agent>] <command line>"
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{usage},
			summary: "Print how the hook would rewrite a shell command line (a debugging aid).",
			examples: []string{
				`ctx-wire rewrite "git status && ls -la | head"`,
				`ctx-wire rewrite --agent opencode "git status"`,
				`ctx-wire rewrite --json --agent pi "git status"`,
			},
			notes: []string{
				"Shows the rewrite only; it runs nothing. For the filter/mode decision, use `ctx-wire explain`.",
			},
		})
		return 0
	}
	agentName := ""
	jsonOutput := false
	for len(args) > 0 {
		switch {
		case args[0] == "--json":
			jsonOutput = true
			args = args[1:]
		case args[0] == "--agent":
			if len(args) < 2 {
				usageLine(os.Stderr, usage)
				return 2
			}
			agentName = args[1]
			args = args[2:]
		case strings.HasPrefix(args[0], "--agent="):
			agentName = strings.TrimPrefix(args[0], "--agent=")
			args = args[1:]
			if agentName == "" {
				usageLine(os.Stderr, usage)
				return 2
			}
		default:
			goto parsed
		}
	}

parsed:
	if len(args) == 0 {
		usageLine(os.Stderr, usage)
		return 2
	}
	line := strings.Join(args, " ")
	if jsonOutput {
		explanation := rewrite.RewriteMetadata(line, agentName)
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(explanation); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire rewrite: %v\n", err)
			return 1
		}
		return 0
	}
	if agentName != "" {
		fmt.Println(rewrite.LineForAgent(line, agentName))
		return 0
	}
	fmt.Println(rewrite.Line(line))
	return 0
}
