package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

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
			usage:   []string{"ctx-wire run <cmd> [args]"},
			summary: "Run a command, then filter and scrub its output before printing it.",
			examples: []string{
				"ctx-wire run git status",
				"ctx-wire run npm ci",
			},
			notes: []string{
				"You rarely run this by hand: agent hooks and PATH shims invoke it for you. The exit code is the command's own.",
			},
		})
		return 0
	}
	if len(args) == 0 {
		usageLine(os.Stderr, "ctx-wire run <cmd> [args]")
		return 2
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
