package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ctx-wire/internal/discover"
)

// cmdDiscover reports commands the agent actually ran (from Claude/Codex local
// transcripts) that ctx-wire never filtered. It is strictly read-only and
// local-only: it reads transcripts and the gain log, makes no network calls, and
// writes nothing.
func cmdDiscover(args []string) int {
	if isHelpArg(args) {
		usageDiscover(os.Stdout)
		return 0
	}
	opts, err := parseDiscoverOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire discover: %v\n", err)
		usageHint(os.Stderr, "ctx-wire discover [--since <dur>] [--top N] [--all]", "discover")
		return 2
	}
	opts.ClaudeDirs = claudeConfigDirs()
	opts.CodexDir = codexHome()

	reg, err := loadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire discover: %v\n", err)
		return 1
	}
	rep, err := discover.Analyze(reg, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire discover: %v\n", err)
		return 1
	}
	fmt.Print(discover.FormatThemed(rep, opts, themeForStdout()))
	return 0
}

// parseDiscoverOptions parses --since, --top, and --all. By default the scan is
// scoped to the current project (the working directory); --all scans every
// project's transcripts.
func parseDiscoverOptions(args []string) (discover.Options, error) {
	var opts discover.Options
	all := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			all = true
		case arg == "--since":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--since requires a value")
			}
			i++
			since, err := parseSince(args[i])
			if err != nil {
				return opts, err
			}
			opts.Since = since
		case strings.HasPrefix(arg, "--since="):
			since, err := parseSince(strings.TrimPrefix(arg, "--since="))
			if err != nil {
				return opts, err
			}
			opts.Since = since
		case arg == "--top":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--top requires a value")
			}
			i++
			n, err := parseTop(args[i])
			if err != nil {
				return opts, err
			}
			opts.TopN = n
		case strings.HasPrefix(arg, "--top="):
			n, err := parseTop(strings.TrimPrefix(arg, "--top="))
			if err != nil {
				return opts, err
			}
			opts.TopN = n
		default:
			return opts, fmt.Errorf("unknown discover option %q", arg)
		}
	}
	if !all {
		if wd, err := os.Getwd(); err == nil {
			opts.Project = wd
		}
	}
	return opts, nil
}

// claudeConfigDirs returns the Claude config directories to scan: the
// CLAUDE_CONFIG_DIR override plus the well-known local dirs that exist.
func claudeConfigDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	add(os.Getenv("CLAUDE_CONFIG_DIR"))
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".claude"))
		add(filepath.Join(home, ".claude-main"))
		add(filepath.Join(home, ".claude-ship"))
	}
	return dirs
}

// codexHome returns the Codex home directory (CODEX_HOME or ~/.codex).
func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".codex")
	}
	return ""
}

func usageDiscover(out *os.File) {
	printHelp(out, helpDoc{
		usage:   []string{"ctx-wire discover [--since <duration|RFC3339>] [--top N] [--all]"},
		summary: "Find agent commands (Claude/Codex transcripts) that ctx-wire never filtered.",
		flags: [][2]string{
			{"--since <dur|ts>", "only consider recent transcript activity"},
			{"--top N", "cap the rows shown"},
			{"--all", "scan every project (default: just the current one)"},
		},
		notes: []string{
			"Read-only and local-only: no network, no writes.",
		},
	})
}
