package main

import (
	"fmt"
	"os"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/ui"
)

// cmdTrust approves the current project's filter file by hash.
func cmdTrust(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire trust"},
			summary: "Approve this project's .ctx-wire/filters.toml so its custom filters load.",
			notes: []string{
				"Records the file's SHA-256. If the file changes, it reverts to untrusted until you re-approve. Revoke with `ctx-wire untrust`.",
			},
		})
		return 0
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire trust: %v\n", err)
		return 1
	}
	path := filter.ProjectFiltersPath(wd)
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire trust: no project filter file at %s\n", path)
		return 1
	}
	h, err := filter.Approve(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire trust: %v\n", err)
		return 1
	}
	theme := themeForStdout()
	fmt.Printf("%s trusted %s\n  %s\n", theme.Success(), theme.Path.Render(path), theme.Dim.Render(h))
	return 0
}

// cmdUntrust revokes trust for the current project's filter file.
func cmdUntrust(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire untrust"},
			summary: "Revoke trust for this project's .ctx-wire/filters.toml (it stops loading until re-approved).",
			notes: []string{
				"Re-approve later with `ctx-wire trust`.",
			},
		})
		return 0
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire untrust: %v\n", err)
		return 1
	}
	path := filter.ProjectFiltersPath(wd)
	removed, err := filter.Revoke(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire untrust: %v\n", err)
		return 1
	}
	theme := themeForStdout()
	if !removed {
		fmt.Printf("%s %s\n", theme.Dim.Render("not trusted"), theme.Path.Render(path))
		return 0
	}
	fmt.Printf("%s untrusted %s\n", theme.Success(), theme.Path.Render(path))
	return 0
}

// cmdVerify runs the inline conformance tests for the built-in filters.
func cmdVerify(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire verify [filter]"},
			summary: "Run the built-in filters' inline conformance tests; pass a name to check just one.",
			examples: []string{
				"ctx-wire verify",
				"ctx-wire verify git-status",
			},
		})
		return 0
	}
	var only string
	if len(args) > 0 {
		only = args[0]
	}

	res, err := filter.VerifyBuiltin(only)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire verify: %v\n", err)
		return 1
	}

	var passed, failed int
	theme := themeForStdout()
	for _, o := range res.Outcomes {
		if o.Passed {
			passed++
			continue
		}
		failed++
		fmt.Printf("%s  %s / %s\n", theme.Fail.Render("FAIL"), theme.Command.Render(o.FilterName), o.TestName)
		fmt.Printf("  %s %q\n", theme.Label.Render("expected:"), o.Expected)
		fmt.Printf("  %s   %q\n", theme.Label.Render("actual:"), o.Actual)
	}

	fmt.Printf("\n%s passed, %s failed (%s tests across filters)\n",
		theme.OK.Render(fmt.Sprintf("%d", passed)),
		statusCount(theme, failed),
		theme.Number.Render(fmt.Sprintf("%d", len(res.Outcomes))))
	if len(res.FiltersWithoutTest) > 0 {
		fmt.Printf("%s (%d): %v\n", theme.Warn.Render("filters without inline tests"), len(res.FiltersWithoutTest), res.FiltersWithoutTest)
	}

	if failed > 0 || len(res.FiltersWithoutTest) > 0 {
		return 1
	}
	return 0
}

func statusCount(theme ui.Theme, n int) string {
	if n == 0 {
		return theme.OK.Render("0")
	}
	return theme.Fail.Render(fmt.Sprintf("%d", n))
}
