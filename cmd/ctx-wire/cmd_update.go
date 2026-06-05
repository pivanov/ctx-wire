package main

import (
	"fmt"
	"os"

	"ctx-wire/internal/selfupdate"
)

// cmdUpdate upgrades ctx-wire to the latest public GitHub release. It is
// explicit and opt-in: it never checks or updates on its own. --check reports
// whether an update is available without installing it.
func cmdUpdate(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire update [--check]"},
			summary: "Upgrade ctx-wire to the latest release (checksum-verified, atomic, with rollback).",
			flags: [][2]string{
				{"--check", "report whether an update is available, without installing it"},
			},
			notes: []string{
				"Explicit and opt-in: ctx-wire never checks for updates on its own. Downloads from public GitHub releases.",
			},
		})
		return 0
	}
	check := false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		default:
			usageHint(os.Stderr, "ctx-wire update [--check]", "update")
			return 2
		}
	}

	res, err := selfupdate.Update(selfupdate.Options{Current: version, Check: check})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire update: %v\n", err)
		return 1
	}
	theme := themeForStdout()
	switch {
	case res.UpToDate:
		fmt.Printf("%s already on the latest release (%s)\n", theme.Success(), res.Current)
	case check:
		fmt.Printf("%s update available: %s -> %s\n", theme.Warn.Render("update"), res.Current, theme.Number.Render(res.Latest))
		fmt.Printf("  %s\n", theme.Dim.Render("run `ctx-wire update` to upgrade"))
	case res.Updated:
		fmt.Printf("%s updated %s -> %s\n", theme.Success(), res.Current, theme.Number.Render(res.Latest))
	}
	return 0
}
