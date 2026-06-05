package main

import (
	"fmt"
	"os"

	"ctx-wire/internal/discover"
)

// cmdSession reports ctx-wire adoption across recent agent transcripts: of the
// commands ctx-wire could route in each session, how many actually went through
// it. Read-only and local-only, like discover.
func cmdSession(args []string) int {
	if isHelpArg(args) {
		usageSession(os.Stdout)
		return 0
	}
	opts, err := parseDiscoverOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire session: %v\n", err)
		usageHint(os.Stderr, "ctx-wire session [--since <dur>] [--top N] [--all]", "session")
		return 2
	}
	if opts.TopN == 0 {
		opts.TopN = 20
	}
	opts.ClaudeDirs = claudeConfigDirs()
	opts.CodexDir = codexHome()

	stats, err := discover.Sessions(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire session: %v\n", err)
		return 1
	}
	fmt.Print(discover.FormatSessionsThemed(stats, themeForStdout()))
	return 0
}

func usageSession(out *os.File) {
	printHelp(out, helpDoc{
		usage:   []string{"ctx-wire session [--since <duration|RFC3339>] [--top N] [--all]"},
		summary: "Per-session ctx-wire adoption across Claude/Codex transcripts (how much went through ctx-wire).",
		flags: [][2]string{
			{"--since <dur|ts>", "only consider recent sessions"},
			{"--top N", "cap the sessions shown"},
			{"--all", "scan every project (default: just the current one)"},
		},
		notes: []string{
			"Read-only and local-only.",
		},
	})
}
