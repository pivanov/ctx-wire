package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/mcpserver"
	"ctx-wire/internal/runner"
)

// cmdMCP runs the stdio MCP server until the client disconnects or the process
// is interrupted.
func cmdMCP(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire mcp"},
			summary: "Serve the filtering tools (run_command, read_file) over MCP on stdio.",
			notes: []string{
				"For MCP-native clients (VS Code, Visual Studio). Register it as an MCP server; it runs until the client disconnects.",
			},
		})
		return 0
	}
	reg, err := loadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := mcpserver.Serve(ctx, reg, version); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp: %v\n", err)
		return 1
	}
	return 0
}

// cmdRun executes a command through the filter pipeline and returns its exit code.
func cmdRun(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire run [--agent <agent>] <cmd> [args]"},
			summary: "Run a command, then filter and scrub its output before printing it.",
			examples: []string{
				"ctx-wire run git status",
				"ctx-wire run --agent claude git status",
				"ctx-wire run npm ci",
			},
			notes: []string{
				"You rarely run this by hand: agent hooks and PATH shims invoke it for you. The exit code is the command's own.",
			},
		})
		return 0
	}
	if len(args) == 0 {
		usageLine(os.Stderr, "ctx-wire run [--agent <agent>] <cmd> [args]")
		return 2
	}
	// --shim is an internal re-entry point used by the Windows .cmd shims: it
	// self-gates (detection, opt-outs, recursion backstop) before filtering. Not
	// in the public help; never typed by a person.
	if args[0] == "--shim" {
		return cmdRunShim(args[1:])
	}
	// --no-dedup forces this command's output to be shown in full even if it is
	// unchanged from a recent run (the recoverable escape hatch for dedup).
	for len(args) > 0 && args[0] == "--no-dedup" {
		os.Setenv("CTX_WIRE_NO_DEDUP", "1")
		args = args[1:]
	}
	if len(args) == 0 {
		usageLine(os.Stderr, "ctx-wire run [--no-dedup] [--agent <agent>] <cmd> [args]")
		return 2
	}
	agentName := ""
	if args[0] == "--agent" {
		if len(args) < 3 {
			usageLine(os.Stderr, "ctx-wire run --agent <agent> <cmd> [args]")
			return 2
		}
		agentName = args[1]
		args = args[2:]
	} else if strings.HasPrefix(args[0], "--agent=") {
		agentName = strings.TrimPrefix(args[0], "--agent=")
		args = args[1:]
		if agentName == "" || len(args) == 0 {
			usageLine(os.Stderr, "ctx-wire run --agent <agent> <cmd> [args]")
			return 2
		}
	}
	if agentName != "" {
		ag := agent.Normalize(agentName)
		if ag == "" {
			fmt.Fprintf(os.Stderr, "ctx-wire run: invalid --agent value %q\n", agentName)
			return 2
		}
		prev, hadPrev := os.LookupEnv(agent.EnvName)
		os.Setenv(agent.EnvName, ag)
		defer func() {
			if hadPrev {
				os.Setenv(agent.EnvName, prev)
			} else {
				os.Unsetenv(agent.EnvName)
			}
		}()
	}
	reg, err := loadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire run: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code, err := runner.Run(ctx, reg, args[0], args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}
