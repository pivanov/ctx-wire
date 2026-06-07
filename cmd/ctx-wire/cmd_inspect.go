package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"ctx-wire/internal/config"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/recent"
	"ctx-wire/internal/ui"
)

// cmdInspect shows raw-vs-filtered for a recent command, so a user can audit what
// ctx-wire removed. It reads the recent-outputs store; when that store is off or
// empty it falls back to recent gain activity so it never feels empty.
func cmdInspect(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage: []string{
				"ctx-wire inspect [n]",
				"ctx-wire inspect --list",
			},
			summary: "Show raw-vs-filtered for a recent command: see exactly what ctx-wire removed.",
			notes: []string{
				"Reads the recent-outputs store, which is OFF by default (it persists successful output). Enable it in config: [retention] enabled = true, and raw_bodies = true for the full raw-vs-filtered audit.",
				"No argument inspects the most recent retained command; `n` picks the nth most recent (1 = newest). `--list` shows what is retained.",
			},
			examples: []string{
				"ctx-wire inspect",
				"ctx-wire inspect 3",
				"ctx-wire inspect --list",
			},
		})
		return 0
	}

	list := false
	idx := 0
	for _, a := range args {
		switch {
		case a == "--list":
			list = true
		default:
			n, err := strconv.Atoi(a)
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "ctx-wire inspect: expected a positive number or --list, got %q\n", a)
				return 2
			}
			idx = n - 1
		}
	}

	theme := themeForStdout()
	entries := recent.List()
	if len(entries) == 0 {
		return inspectEmpty(theme)
	}
	if list {
		inspectList(theme, entries)
		return 0
	}
	if idx >= len(entries) {
		fmt.Fprintf(os.Stderr, "ctx-wire inspect: only %d recent entries retained (use --list)\n", len(entries))
		return 1
	}
	renderInspect(theme, entries[len(entries)-1-idx])
	return 0
}

func renderInspect(theme ui.Theme, e recent.Entry) {
	saved := e.RawBytes - e.EmitBytes
	if saved < 0 {
		saved = 0
	}
	pct := 0.0
	if e.RawBytes > 0 {
		pct = 100 * float64(saved) / float64(e.RawBytes)
	}
	filterName := e.Filter
	if filterName == "" {
		filterName = "(none)"
	}

	fmt.Printf("%s %s\n", theme.Label.Render("inspect:"), theme.Command.Render(e.Command))
	fmt.Printf("  %s %s (%s)   %s %s   %s %d\n",
		theme.Dim.Render("filter:"), filterName, e.Mode,
		theme.Dim.Render("when:"), e.TS,
		theme.Dim.Render("exit:"), e.Exit)
	fmt.Printf("  %s %d B -> %d B  (%.1f%% saved)\n", theme.Dim.Render("size:"), e.RawBytes, e.EmitBytes, pct)

	fmt.Printf("\n%s\n%s\n", theme.Dim.Render("--- filtered (what the agent saw) ---"), e.Emitted)
	if e.Raw != "" {
		fmt.Printf("\n%s\n%s\n", theme.Dim.Render("--- raw (before filtering) ---"), e.Raw)
		if n := removedLines(e.Raw, e.Emitted); n > 0 {
			fmt.Printf("\n%s the filter removed %d line(s) above\n", theme.Dim.Render("note:"), n)
		}
	} else {
		fmt.Printf("\n%s raw body not retained; set retention.raw_bodies = true for the full raw-vs-filtered audit\n", theme.Dim.Render("note:"))
	}
}

func inspectList(theme ui.Theme, entries []recent.Entry) {
	fmt.Printf("%s\n", theme.Dim.Render("retained recent commands (newest first):"))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		tier := ""
		if e.Raw == "" {
			tier = " (lean)"
		}
		fmt.Printf("  %2d. %s  %dB->%dB%s  %s\n", len(entries)-i, e.Command, e.RawBytes, e.EmitBytes, tier, e.TS)
	}
}

// inspectEmpty is the first-use path: explain how to turn retention on and still
// show recent activity from the gain log, so the command never feels empty.
func inspectEmpty(theme ui.Theme) int {
	cfg, _ := config.Load()
	if !cfg.Retention.Enabled {
		fmt.Printf("%s the recent-outputs store is off, so there is nothing to inspect yet.\n", theme.Warn.Render("inspect:"))
		fmt.Print("  Enable it to audit what ctx-wire removes from successful commands:\n\n")
		fmt.Print("    [retention]\n    enabled = true\n    raw_bodies = true   # for the full raw-vs-filtered audit\n\n")
	} else {
		fmt.Printf("%s no commands retained yet; run a filtered command and try again.\n\n", theme.Warn.Render("inspect:"))
	}

	if entries, err := gain.RecentEntries(8); err == nil && len(entries) > 0 {
		fmt.Printf("%s\n", theme.Dim.Render("recent commands (from the gain log):"))
		for _, e := range entries {
			fmt.Printf("  %s  saved %d B\n", e.Command, e.SavedBytes)
		}
	}
	return 0
}

// removedLines counts lines present in raw but not in emitted (multiset diff): a
// quick "what did the filter cut" signal, not a precise diff.
func removedLines(raw, emitted string) int {
	kept := map[string]int{}
	for _, l := range strings.Split(emitted, "\n") {
		kept[l]++
	}
	removed := 0
	for _, l := range strings.Split(raw, "\n") {
		if kept[l] > 0 {
			kept[l]--
		} else {
			removed++
		}
	}
	return removed
}
