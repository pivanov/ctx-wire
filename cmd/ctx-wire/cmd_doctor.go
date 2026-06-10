package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"ctx-wire/internal/doctor"
)

// cmdDoctor runs the read-only self-diagnostic. Exit 0 when healthy (warnings
// allowed), 1 when a failing check (broken install / unwritable storage /
// unloadable registry) is present.
func cmdDoctor(args []string) int {
	if isHelpArg(args) {
		usageDoctor(os.Stdout)
		return 0
	}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verbose := fs.Bool("verbose", false, "show recent scrubbed commands (implies --recent 5)")
	recent := fs.Int("recent", 0, "show the N most recent scrubbed commands")
	all := fs.Bool("all", false, "also show optional [off] checks (integrations not set up)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire doctor: %v\n", err)
		usageHint(os.Stderr, "ctx-wire doctor [--all] [--recent N] [--verbose]", "doctor")
		return 2
	}

	n := *recent
	if *verbose && n == 0 {
		n = 5
	}

	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	report := doctor.Run(doctor.Options{
		Version: version,
		Commit:  commit,
		Date:    date,
		Workdir: wd,
		Recent:  n,
	})
	fmt.Print(doctor.FormatThemed(report, themeForStdout(), *all))
	if report.Healthy() {
		return 0
	}
	return 1
}

func usageDoctor(out *os.File) {
	printHelp(out, helpDoc{
		usage:   []string{"ctx-wire doctor [--all] [--recent N] [--verbose]"},
		summary: "Health check: binary, PATH, agent hooks, shims, storage, filters, trust, and telemetry.",
		flags: [][2]string{
			{"--all", "also show optional [off] checks (hidden by default)"},
			{"--recent N", "also show the N most recent recorded commands"},
			{"--verbose", "show extra detail for each check"},
		},
		notes: []string{
			"Read-only. A good first stop when something is not filtering as expected.",
		},
	})
}
