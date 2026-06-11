package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/install"
	"ctx-wire/internal/paths"
	"ctx-wire/internal/shim"
)

// cmdUninstall removes every local trace of ctx-wire: its binary, managed shims,
// its own agent hook entries, and its dedicated config and data directories
// (config, filters, trust, gain and shim logs, tee captures, telemetry config
// and state). It edits shared agent files surgically, removing only ctx-wire's
// own blocks, and never touches unrelated files.
func cmdUninstall(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire uninstall [agent]"},
			summary: "Remove ctx-wire. No argument: the binary, managed shims, config/data, and ctx-wire's entries from every agent. An agent name: only that agent's wiring.",
			notes: []string{
				"No argument is full removal: ctx-wire's own config and data directories (gain logs, tee captures, trust records, telemetry config/state) are deleted.",
				"With an agent (e.g. `ctx-wire uninstall claude`): only that agent's hook/plugin/rules wiring is removed; the binary, shims, config/data, and every other agent are left intact.",
				"Shared agent files are edited surgically: only the blocks ctx-wire added are removed; the rest of your settings and any custom files are left untouched.",
			},
		})
		return 0
	}
	if len(args) == 1 {
		return cmdUninstallAgent(args[0])
	}
	if len(args) > 1 {
		usageLine(os.Stderr, "ctx-wire uninstall [agent]")
		return 2
	}
	dest, err := install.SelfInstallPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall: %v\n", err)
		return 1
	}
	theme := themeForStdout()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall: %v\n", err)
		return 1
	}
	integrations, err := install.UninstallIntegrations(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall: %v\n", err)
		return 1
	}
	if len(integrations.Removed) > 0 {
		fmt.Printf("%s ctx-wire hook/config entries: %s\n", theme.OK.Render("Removed"), strings.Join(integrations.Removed, ", "))
	} else {
		fmt.Printf("%s no ctx-wire hook/config entries found\n", theme.OK.Render("OK"))
	}
	if len(integrations.Skipped) > 0 {
		fmt.Printf("%s left custom hook files alone: %s\n", theme.Warn.Render("Skipped"), strings.Join(integrations.Skipped, ", "))
	}

	// Revert MCP wraps BEFORE the binary is removed: a wrapped server entry
	// launches through this executable, so leaving it would brick that server
	// the moment the binary disappears.
	unwrapped, err := unwrapAllCtxWireMCP("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall: mcp unwrap: %v\n", err)
		return 1
	}
	if len(unwrapped) > 0 {
		fmt.Printf("%s MCP wrap from server(s): %s (original launch restored)\n", theme.OK.Render("Removed"), strings.Join(unwrapped, ", "))
	}

	report, err := shim.UninstallDefault(filepath.Dir(dest))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall: %v\n", err)
		return 1
	}
	if len(report.Removed) > 0 {
		fmt.Printf("%s %d managed command shims from %s\n", theme.OK.Render("Removed"), len(report.Removed), theme.Path.Render(report.Dir))
	} else {
		fmt.Printf("%s no managed command shims found in %s\n", theme.OK.Render("OK"), theme.Path.Render(report.Dir))
	}
	if len(report.Skipped) > 0 {
		fmt.Printf("%s left existing non-ctx-wire files alone: %s\n", theme.Warn.Render("Skipped"), strings.Join(report.Skipped, ", "))
	}

	removed, err := install.UninstallSelf(dest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall: %v\n", err)
		return 1
	}
	if removed {
		fmt.Printf("%s ctx-wire from %s\n", theme.OK.Render("Removed"), theme.Path.Render(dest))
	} else {
		fmt.Printf("%s ctx-wire binary not present at %s\n", theme.OK.Render("OK"), theme.Path.Render(dest))
	}

	// Purge ctx-wire's own config and data directories. Everything inside them
	// (config, filters, trust, gain/shim logs, tee captures, telemetry config
	// and state) is ctx-wire-only, so a wholesale RemoveAll clears every local
	// trace without risking unrelated files.
	dirs, err := paths.OwnedDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall: %v\n", err)
		return 1
	}
	var purged []string
	for _, d := range dirs {
		if _, statErr := os.Stat(d); statErr != nil {
			continue // not present (or unreadable): nothing to remove here
		}
		if err := os.RemoveAll(d); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire uninstall: %v\n", err)
			return 1
		}
		purged = append(purged, d)
	}
	if len(purged) > 0 {
		fmt.Printf("%s ctx-wire config and data: %s\n", theme.OK.Render("Removed"), strings.Join(purged, ", "))
	} else {
		fmt.Printf("%s no ctx-wire config or data directories found\n", theme.OK.Render("OK"))
	}

	fmt.Println("Unrelated agent settings and custom files were left intact.")
	return 0
}

// cmdUninstallAgent removes only one agent's ctx-wire wiring (its
// hook/plugin/rules and instruction block), leaving the binary, managed shims,
// ctx-wire config/data, and every other agent intact.
func cmdUninstallAgent(raw string) int {
	name := canonicalUninstallAgent(raw)
	if !agent.IsKnown(name) {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall: unknown agent %q\n", raw)
		fmt.Fprintf(os.Stderr, "known agents: %s\n", strings.Join(agent.Known, ", "))
		return 2
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall %s: %v\n", name, err)
		return 1
	}
	theme := themeForStdout()
	report, err := install.UninstallAgent(wd, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire uninstall %s: %v\n", name, err)
		return 1
	}
	if len(report.Removed) > 0 {
		fmt.Printf("%s %s wiring: %s\n", theme.OK.Render("Removed"), name, strings.Join(report.Removed, ", "))
	} else {
		fmt.Printf("%s no ctx-wire wiring found for %s\n", theme.OK.Render("OK"), name)
	}
	if len(report.Skipped) > 0 {
		fmt.Printf("%s left custom files alone: %s\n", theme.Warn.Render("Skipped"), strings.Join(report.Skipped, ", "))
	}
	fmt.Println(theme.Dim.Render("binary, shims, config/data, and other agents left intact"))
	fmt.Println(theme.Dim.Render("MCP wraps, if any, revert separately: ctx-wire mcp-wrap uninstall <server>"))
	return 0
}

func canonicalUninstallAgent(raw string) string {
	return canonicalInitAgent(agent.Normalize(raw))
}
