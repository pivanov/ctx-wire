package main

import (
	"fmt"
	"os"
	"strings"

	"ctx-wire/internal/explain"
)

// cmdExplain diagnoses a single command: which filter and runner mode ctx-wire
// applies, and whether the hook wraps it. The global token-opportunity report
// lives in `ctx-wire tune`, so the two no longer print the same thing. Read-only.
func cmdExplain(args []string) int {
	if isHelpArg(args) || len(args) == 0 {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire explain <cmd>"},
			summary: "Diagnose how ctx-wire handles a command: which filter and mode, and whether the hook wraps it.",
			examples: []string{
				"ctx-wire explain git status",
				`ctx-wire explain "rg TODO . | wc -l"`,
			},
			notes: []string{
				"Read-only. For the token-opportunity / filter-improvement report across all commands, run `ctx-wire tune`.",
			},
		})
		return 0
	}
	reg, err := loadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire explain: %v\n", err)
		return 1
	}
	line := strings.Join(args, " ")
	fmt.Print(explain.FormatCommandThemed(explain.Command(reg, line), themeForStdout()))
	return 0
}
