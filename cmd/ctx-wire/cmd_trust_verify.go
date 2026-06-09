package main

import (
	"fmt"
	"os"
	"strings"

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

// cmdVerify runs inline conformance tests: the built-in filters by default, or a
// local file with --project / --file. Local verification is trust-free.
func cmdVerify(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage: []string{
				"ctx-wire verify [filter]",
				"ctx-wire verify --project [filter]",
				"ctx-wire verify --file <path> [filter]",
			},
			summary: "Run filters' inline conformance tests; built-ins by default, or a local file.",
			examples: []string{
				"ctx-wire verify",
				"ctx-wire verify git-status",
				"ctx-wire verify --project",
			},
			notes: []string{
				"--project verifies .ctx-wire/filters.toml; --file verifies a path. Both are trust-free (they run the file's own inline tests, they do not load or apply it).",
				"A draft test (draft = true) fails verification until you trim expected and remove the marker; --allow-draft permits it for a local file. Built-ins never may carry one.",
			},
		})
		return 0
	}

	var (
		project    bool
		allowDraft bool
		file       string
		only       string
	)
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--project":
			project = true
		case a == "--allow-draft":
			allowDraft = true
		case a == "--file":
			if i+1 >= len(args) {
				usageLine(os.Stderr, "ctx-wire verify --file <path> [filter]")
				return 2
			}
			i++
			file = args[i]
		case strings.HasPrefix(a, "--file="):
			file = strings.TrimPrefix(a, "--file=")
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "ctx-wire verify: unknown flag %q\n", a)
			return 2
		default:
			only = a
		}
	}

	local := project || file != ""

	var (
		res *filter.VerifyResults
		err error
	)
	switch {
	case file != "":
		res, err = filter.VerifyFile(file, only)
	case project:
		wd, werr := os.Getwd()
		if werr != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire verify: %v\n", werr)
			return 1
		}
		res, err = filter.VerifyFile(filter.ProjectFiltersPath(wd), only)
	default:
		res, err = filter.VerifyBuiltin(only)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire verify: %v\n", err)
		return 1
	}

	var passed, failed, drafts int
	theme := themeForStdout()
	fmt.Println(theme.Heading("ctx-wire verify: conformance"))
	for _, o := range res.Outcomes {
		if o.Draft {
			drafts++
		}
		if o.Passed {
			passed++
			if o.Draft {
				fmt.Printf("%s  %s / %s (asserts nothing yet; trim expected, remove draft = true)\n",
					theme.Warn.Render("DRAFT"), theme.Command.Render(o.FilterName), o.TestName)
			}
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

	if failed > 0 {
		return 1
	}
	// A draft marker fails verification: a built-in must never ship one, and a
	// local file is only allowed to keep it with the explicit --allow-draft.
	if drafts > 0 {
		if local && allowDraft {
			fmt.Printf("%s %d draft test(s) allowed via --allow-draft\n", theme.Warn.Render("note:"), drafts)
		} else {
			if !local {
				fmt.Printf("%s a built-in filter must not ship a draft test\n", theme.Fail.Render("draft:"))
			}
			return 1
		}
	}
	// Built-in runs also flag filters that have no inline tests at all.
	if !local && len(res.FiltersWithoutTest) > 0 {
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
