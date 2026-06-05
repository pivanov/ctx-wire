package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/tune"
)

// cmdTune prints a higher-level, local-only filter improvement report built from
// the recorded gain data. It is read-only: it never runs commands, captures
// samples, writes files, or makes network calls.
func cmdTune(args []string) int {
	if isHelpArg(args) {
		usageTune(os.Stdout)
		return 0
	}
	if len(args) > 0 && args[0] == "preview" {
		return cmdTunePreview(args[1:])
	}
	if len(args) > 0 && args[0] == "bundle" {
		return cmdTuneBundle(args[1:])
	}
	if len(args) > 0 && args[0] == "issue" {
		return cmdTuneIssue(args[1:])
	}
	gopts, topts, err := parseTuneOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune: %v\n", err)
		usageHint(os.Stderr, "ctx-wire tune [--since <dur>] [--top N] | preview | bundle | issue", "tune")
		return 2
	}
	s, err := gain.SummarizeWithOptions(gopts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune: %v\n", err)
		return 1
	}
	// Pass the live registry so tune drops stale "missing filter" entries that the
	// current filters already match (best-effort: a load error just skips that).
	reg, _ := loadRegistry()
	fmt.Print(tune.FormatThemed(tune.Analyze(reg, s), topts, themeForStdout()))
	return 0
}

// cmdTunePreview shows what `ctx-wire tune bundle` would include, with
// every sample command sanitized. It is strictly read-only: it writes no files,
// captures no output, and makes no network calls.
func cmdTunePreview(args []string) int {
	if isHelpArg(args) {
		usageTune(os.Stdout)
		return 0
	}
	gopts, topts, err := parseTuneOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune preview: %v\n", err)
		usageHint(os.Stderr, "ctx-wire tune [--since <dur>] [--top N] | preview | bundle | issue", "tune")
		return 2
	}
	s, err := gain.SummarizeWithOptions(gopts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune preview: %v\n", err)
		return 1
	}
	san := newSanitizerFromEnv()
	fmt.Print(tune.FormatPreviewThemed(tune.BuildPreview(s, san, topts), themeForStdout()))
	return 0
}

// newSanitizerFromEnv builds a Sanitizer using the user home directory and the
// current working directory as the project root.
func newSanitizerFromEnv() tune.Sanitizer {
	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	return tune.NewSanitizer(home, wd)
}

// cmdTuneBundle writes a sanitized .tar.gz archive of the tune data for manual
// sharing. It captures no raw command output and makes no network calls. The
// only write is the single archive file; its path is printed with a reminder to
// inspect it before sharing.
func cmdTuneBundle(args []string) int {
	if isHelpArg(args) {
		usageTune(os.Stdout)
		return 0
	}
	gopts, topts, outPath, err := parseBundleArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune bundle: %v\n", err)
		usageHint(os.Stderr, "ctx-wire tune [--since <dur>] [--top N] | preview | bundle | issue", "tune")
		return 2
	}
	s, err := gain.SummarizeWithOptions(gopts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune bundle: %v\n", err)
		return 1
	}
	data, err := tune.BuildBundle(s, newSanitizerFromEnv(), topts, bundleMetaFromEnv(gopts))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune bundle: %v\n", err)
		return 1
	}
	if outPath == "" {
		outPath = "ctx-wire-tune.tar.gz"
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune bundle: %v\n", err)
		return 1
	}
	abs, err := filepath.Abs(outPath)
	if err != nil {
		abs = outPath
	}
	theme := themeForStdout()
	fmt.Printf("%s wrote %s %s\n", theme.OK.Render("OK"), theme.Path.Render(abs),
		theme.Dim.Render(fmt.Sprintf("(%d bytes)", len(data))))
	fmt.Printf("  %s\n", theme.Dim.Render("inspect the contents before sharing; it contains scrubbed sample commands only, no raw output"))
	fmt.Printf("  %s\n", theme.Dim.Render("No network calls were made."))
	return 0
}

// parseBundleArgs extracts the bundle-specific --out flag, then parses the
// remaining flags with parseTuneOptions so --since/--top behave identically.
func parseBundleArgs(args []string) (gain.Options, tune.Options, string, error) {
	var rest []string
	out := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--out":
			if i+1 >= len(args) {
				return gain.Options{}, tune.Options{}, "", fmt.Errorf("--out requires a value")
			}
			i++
			out = args[i]
		case strings.HasPrefix(arg, "--out="):
			out = strings.TrimPrefix(arg, "--out=")
			if out == "" {
				return gain.Options{}, tune.Options{}, "", fmt.Errorf("--out requires a value")
			}
		default:
			rest = append(rest, arg)
		}
	}
	gopts, topts, err := parseTuneOptions(rest)
	return gopts, topts, out, err
}

// bundleMetaFromEnv gathers the injected bundle metadata. Filter and conformance
// counts are cheap to obtain and best-effort: a load error simply omits them.
func bundleMetaFromEnv(gopts gain.Options) tune.BundleMeta {
	meta := tune.BundleMeta{
		GeneratedAt: time.Now().UTC(),
		Version:     version,
		Commit:      commit,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Window:      windowDesc(gopts.Since),
	}
	if reg, err := filter.LoadBuiltin(); err == nil {
		meta.FilterCount = len(reg.Filters)
	}
	if res, err := filter.VerifyBuiltin(""); err == nil {
		meta.ConformanceCount = len(res.Outcomes)
	}
	return meta
}

func windowDesc(since time.Time) string {
	if since.IsZero() {
		return "all time"
	}
	return "since " + since.UTC().Format(time.RFC3339)
}

// cmdTuneIssue prints a Markdown GitHub issue body from the sanitized tune data.
// By default it is fully read-only (no files, no browser, no network). With
// --open it builds a prefilled GitHub "new issue" URL and launches the browser;
// it never calls the GitHub API, stores a token, or submits anything.
func cmdTuneIssue(args []string) int {
	if isHelpArg(args) {
		usageTune(os.Stdout)
		return 0
	}
	gopts, topts, iopts, err := parseIssueArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune issue: %v\n", err)
		usageHint(os.Stderr, "ctx-wire tune [--since <dur>] [--top N] | preview | bundle | issue", "tune")
		return 2
	}
	s, err := gain.SummarizeWithOptions(gopts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune issue: %v\n", err)
		return 1
	}
	issue := tune.BuildIssue(s, newSanitizerFromEnv(), topts, bundleMetaFromEnv(gopts), iopts.bundle)
	if !iopts.open {
		printIssue(issue)
		return 0
	}
	return openIssue(issue, iopts.repo)
}

// issueOptions holds the issue-specific flags.
type issueOptions struct {
	open   bool
	repo   string
	bundle string
}

// printIssue writes the suggested title and Markdown body to stdout. No files
// are written.
func printIssue(issue tune.Issue) {
	fmt.Printf("Suggested title: %s\n\n", issue.Title)
	fmt.Print(issue.Body)
	if !strings.HasSuffix(issue.Body, "\n") {
		fmt.Println()
	}
}

// tuneUpstreamRepo is the canonical ctx-wire repository that `tune issue`
// targets, so a filter-gap report reaches the ctx-wire maintainers no matter
// which project you run it from. A power user can override it with --repo (e.g.
// to file against a fork).
const tuneUpstreamRepo = "pivanov/ctx-wire"

// openIssue opens a prefilled GitHub new-issue URL in the browser against the
// ctx-wire repo (or --repo). It makes no GitHub API calls; the user reviews and
// submits manually. On failure (over-long URL, browser launch error) it falls
// back to printing the issue so nothing is lost.
func openIssue(issue tune.Issue, repoFlag string) int {
	repo := repoFlag
	if repo == "" {
		repo = tuneUpstreamRepo
	}

	u, ok := tune.IssueURL(repo, issue)
	if !ok {
		fmt.Fprintln(os.Stderr, "ctx-wire tune issue: the prefilled issue is too large to pass through a browser URL")
		fmt.Fprintln(os.Stderr, "  copy the issue body below into a new issue manually")
		fmt.Println()
		printIssue(issue)
		return 0
	}

	theme := themeForStdout()
	fmt.Printf("%s the browser URL below contains the full (sanitized) issue body; review it before submitting\n",
		theme.Warn.Render("note:"))
	fmt.Printf("  %s\n", theme.Dim.Render("ctx-wire only opens the page prefilled; you must click Submit yourself. No data is uploaded by ctx-wire."))
	fmt.Printf("opening %s\n", theme.Path.Render("https://github.com/"+repo+"/issues/new"))
	if err := openBrowser(u); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune issue: could not open a browser: %v\n", err)
		fmt.Fprintln(os.Stderr, "  open this URL manually:")
		fmt.Println(u)
		return 1
	}
	return 0
}

// parseIssueArgs extracts the issue-specific flags (--open, --repo, --bundle),
// then parses the remaining flags with parseTuneOptions so --since/--top behave
// identically.
func parseIssueArgs(args []string) (gain.Options, tune.Options, issueOptions, error) {
	var rest []string
	var iopts issueOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--open":
			iopts.open = true
		case arg == "--repo":
			if i+1 >= len(args) {
				return gain.Options{}, tune.Options{}, iopts, fmt.Errorf("--repo requires a value")
			}
			i++
			iopts.repo = args[i]
		case strings.HasPrefix(arg, "--repo="):
			iopts.repo = strings.TrimPrefix(arg, "--repo=")
			if iopts.repo == "" {
				return gain.Options{}, tune.Options{}, iopts, fmt.Errorf("--repo requires a value")
			}
		case arg == "--bundle":
			if i+1 >= len(args) {
				return gain.Options{}, tune.Options{}, iopts, fmt.Errorf("--bundle requires a value")
			}
			i++
			iopts.bundle = args[i]
		case strings.HasPrefix(arg, "--bundle="):
			iopts.bundle = strings.TrimPrefix(arg, "--bundle=")
			if iopts.bundle == "" {
				return gain.Options{}, tune.Options{}, iopts, fmt.Errorf("--bundle requires a value")
			}
		default:
			rest = append(rest, arg)
		}
	}
	gopts, topts, err := parseTuneOptions(rest)
	return gopts, topts, iopts, err
}

// parseTuneOptions parses the tune flags into gain windowing options (--since)
// and tune render options (--top). It reuses parseSince so --since behaves
// exactly like `ctx-wire gain --since`.
func parseTuneOptions(args []string) (gain.Options, tune.Options, error) {
	var gopts gain.Options
	var topts tune.Options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--since":
			if i+1 >= len(args) {
				return gopts, topts, fmt.Errorf("--since requires a value")
			}
			i++
			since, err := parseSince(args[i])
			if err != nil {
				return gopts, topts, err
			}
			gopts.Since = since
		case strings.HasPrefix(arg, "--since="):
			since, err := parseSince(strings.TrimPrefix(arg, "--since="))
			if err != nil {
				return gopts, topts, err
			}
			gopts.Since = since
		case arg == "--top":
			if i+1 >= len(args) {
				return gopts, topts, fmt.Errorf("--top requires a value")
			}
			i++
			n, err := parseTop(args[i])
			if err != nil {
				return gopts, topts, err
			}
			topts.TopN = n
		case strings.HasPrefix(arg, "--top="):
			n, err := parseTop(strings.TrimPrefix(arg, "--top="))
			if err != nil {
				return gopts, topts, err
			}
			topts.TopN = n
		default:
			return gopts, topts, fmt.Errorf("unknown tune option %q", arg)
		}
	}
	return gopts, topts, nil
}

func parseTop(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid --top %q (use a non-negative integer)", value)
	}
	return n, nil
}

func usageTune(out *os.File) {
	printHelp(out, helpDoc{
		usage: []string{
			"ctx-wire tune [--since <duration|RFC3339>] [--top N]",
			"ctx-wire tune preview [--since ...] [--top N]",
			"ctx-wire tune bundle [--out PATH] [--since ...] [--top N]",
			"ctx-wire tune issue [--open] [--since ...] [--top N]",
		},
		summary: "Report filter gaps and weak filters from your gain log; package a sanitized bundle to share.",
		commands: [][2]string{
			{"preview", "show what a bundle would include, without writing it"},
			{"bundle", "write a sanitized bundle archive for manual sharing"},
			{"issue", "print, or with --open file, a sanitized filter-gap report to ctx-wire"},
		},
		flags: [][2]string{
			{"--since <dur|ts>", "only consider recent gain data"},
			{"--top N", "cap the rows shown"},
			{"--out PATH", "bundle output path (bundle)"},
			{"--open", "open the issue draft in a browser (issue)"},
		},
		examples: []string{
			"ctx-wire tune",
			"ctx-wire tune bundle --out tune.tar.gz",
		},
		notes: []string{
			"With no subcommand it prints the local report. Read-only unless you run `bundle`; no network, no auto-upload.",
		},
	})
}
