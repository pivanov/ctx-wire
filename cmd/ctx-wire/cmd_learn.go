package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"ctx-wire/internal/learn"
)

// cmdLearn mines local Claude transcripts for repeated CLI mistakes and prints
// the corrections it found. With --write it also persists them to
// .claude/rules/cli-corrections.md in the current project. It is read-only
// against transcripts and writes nothing unless --write is given.
func cmdLearn(args []string) int {
	if isHelpArg(args) {
		usageLearn(os.Stdout)
		return 0
	}
	opts, write, err := parseLearnOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire learn: %v\n", err)
		usageHint(os.Stderr, "ctx-wire learn [--since <dur>] [--all] [--min N] [--write]", "learn")
		return 2
	}
	opts.ClaudeDirs = claudeConfigDirs()

	rep, err := learn.Analyze(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire learn: %v\n", err)
		return 1
	}
	fmt.Print(learn.FormatThemed(rep, themeForStdout()))

	if write {
		root, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire learn: %v\n", err)
			return 1
		}
		path, err := learn.WriteRulesFile(rep, root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire learn: %v\n", err)
			return 1
		}
		fmt.Printf("%s wrote %s\n", themeForStdout().OK.Render("OK"), path)
	}
	return 0
}

// parseLearnOptions parses --since, --all, --write, and --min. By default the
// scan is scoped to the current project; --all scans every project's sessions.
func parseLearnOptions(args []string) (opts learn.Options, write bool, err error) {
	all := false
	opts.MinOccurrences = 1
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			all = true
		case arg == "--write":
			write = true
		case arg == "--since":
			if i+1 >= len(args) {
				return opts, false, fmt.Errorf("--since requires a value")
			}
			i++
			since, perr := parseSince(args[i])
			if perr != nil {
				return opts, false, perr
			}
			opts.Since = since
		case strings.HasPrefix(arg, "--since="):
			since, perr := parseSince(strings.TrimPrefix(arg, "--since="))
			if perr != nil {
				return opts, false, perr
			}
			opts.Since = since
		case arg == "--min":
			if i+1 >= len(args) {
				return opts, false, fmt.Errorf("--min requires a value")
			}
			i++
			n, perr := strconv.Atoi(args[i])
			if perr != nil || n < 1 {
				return opts, false, fmt.Errorf("invalid --min %q (want a positive integer)", args[i])
			}
			opts.MinOccurrences = n
		case strings.HasPrefix(arg, "--min="):
			n, perr := strconv.Atoi(strings.TrimPrefix(arg, "--min="))
			if perr != nil || n < 1 {
				return opts, false, fmt.Errorf("invalid --min value (want a positive integer)")
			}
			opts.MinOccurrences = n
		default:
			return opts, false, fmt.Errorf("unknown learn option %q", arg)
		}
	}
	if !all {
		if wd, werr := os.Getwd(); werr == nil {
			opts.Project = wd
		}
	}
	return opts, write, nil
}

func usageLearn(out *os.File) {
	printHelp(out, helpDoc{
		usage:   []string{"ctx-wire learn [--since <duration|RFC3339>] [--all] [--min N] [--write]"},
		summary: "Mine local Claude transcripts for failed->corrected commands and turn them into rule hints.",
		flags: [][2]string{
			{"--since <dur|ts>", "only consider recent transcripts"},
			{"--all", "scan every project (default: just the current one)"},
			{"--min N", "only report corrections seen at least N times (default 1)"},
			{"--write", "save the rules to .claude/rules/cli-corrections.md"},
		},
		notes: []string{
			"Read-only unless --write is given. Commands and error snippets are scrubbed.",
		},
	})
}
