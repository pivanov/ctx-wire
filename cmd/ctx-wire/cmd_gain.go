package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"ctx-wire/internal/config"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/tee"
	"ctx-wire/internal/telemetry"
	"ctx-wire/internal/ui"

	"github.com/charmbracelet/lipgloss"
)

// cmdGain prints or clears the recorded token-savings summary.
func cmdGain(args []string) int {
	if isHelpArg(args) {
		usageGain(os.Stdout)
		return 0
	}
	if len(args) > 0 && args[0] == "clear" {
		if len(args) != 1 {
			usageLine(os.Stderr, "ctx-wire gain clear")
			return 2
		}
		if err := gain.Clear(); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire gain clear: %v\n", err)
			return 1
		}
		_ = telemetry.ClearState()
		fmt.Printf("%s gain history cleared\n", themeForStdout().Success())
		return 0
	}
	view, opts, limit, q, err := parseGainOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire gain: %v\n", err)
		usageHint(os.Stderr, "ctx-wire gain [--since <dur>] [--history|--daily|--weekly|--monthly|--graph|--json|--csv|--quota|clear]", "gain")
		return 2
	}

	if view == "quota" {
		return gainQuota(opts, q)
	}

	// Alternate views read raw entries; they never report telemetry.
	if view != "" {
		entries, err := gain.Entries(opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire gain: %v\n", err)
			return 1
		}
		switch view {
		case "history":
			fmt.Print(gain.FormatHistoryThemed(entries, limit, themeForStdout()))
		case "daily":
			fmt.Print(gain.FormatPeriodThemed("daily", gain.Daily(entries), themeForStdout()))
		case "weekly":
			fmt.Print(gain.FormatPeriodThemed("weekly", gain.Weekly(entries), themeForStdout()))
		case "monthly":
			fmt.Print(gain.FormatPeriodThemed("monthly", gain.Monthly(entries), themeForStdout()))
		case "graph":
			fmt.Print(gain.FormatGraphThemed(gain.Daily(entries), themeForStdout()))
		case "csv":
			fmt.Print(gain.FormatCSV(gain.Daily(entries)))
		case "json":
			s, err := gain.SummarizeWithOptions(opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ctx-wire gain: %v\n", err)
				return 1
			}
			out, err := gain.FormatJSON(s, gain.Daily(entries))
			if err != nil {
				fmt.Fprintf(os.Stderr, "ctx-wire gain: %v\n", err)
				return 1
			}
			fmt.Print(out)
		}
		return 0
	}

	s, err := gain.SummarizeWithOptions(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire gain: %v\n", err)
		return 1
	}
	// One-time telemetry notice: tell the human, once, that opt-out telemetry is
	// on and how to turn it off. Gated on BOTH stdin and stdout being a terminal,
	// so an agent or a pipe is never prompted (and never blocked waiting for input).
	if ui.IsTerminal(os.Stdin) && ui.IsTerminal(os.Stdout) {
		if telemetry.ShouldPreviewConsent() {
			promptConsent()
		}
	} else if notice := telemetry.MigrationNoticeIfPending(); notice != "" {
		// Non-interactive (agent) path: disclose the opt-out migration at the point
		// of collection with a one-time stderr line. Its marker is separate from the
		// interactive notice, so a swallowed line here never suppresses the one a
		// human sees in a terminal.
		fmt.Fprintln(os.Stderr, "ctx-wire: "+notice)
	}
	fmt.Print(gain.FormatThemed(s, themeForStdout()))
	printFetchStats()
	if opts.Since.IsZero() {
		_, _ = telemetry.ReportImpact(s)
	}
	return 0
}

// printFetchStats appends the aggregate fetch-redemption counter to gain output
// when at least one fetch has been recorded. Errors loading the stats are silently
// ignored (the counter is informational).
func printFetchStats() {
	fs, err := tee.ReadFetchStats()
	if err != nil {
		return
	}
	total := fs.Full + fs.Ranged + fs.Miss
	if total == 0 {
		return
	}
	theme := themeForStdout()
	fmt.Printf("\n%s\n", theme.Section.Render("Fetch redemptions (all-time)"))
	if fs.Full > 0 {
		fmt.Printf("  %-22s %s\n", "Full fetches:", fmt.Sprintf("%d", fs.Full))
	}
	if fs.Ranged > 0 {
		fmt.Printf("  %-22s %s  (%s lines)\n", "Ranged fetches:", fmt.Sprintf("%d", fs.Ranged), fmt.Sprintf("%d", fs.LinesReturned))
	}
	if fs.Miss > 0 {
		fmt.Printf("  %-22s %s\n", "Misses/evicted:", fmt.Sprintf("%d", fs.Miss))
	}
	if fs.BytesReturned > 0 {
		fmt.Printf("  %-22s %s\n", "Bytes returned:", ui.HumanBytes(fs.BytesReturned))
	}
}

// promptConsent shows a fixed EXAMPLE of the anonymous payload and informs the
// human, once, that opt-out telemetry is ON. Pressing Enter keeps it on (the
// opt-out default); an explicit "no" disables it, which is then honored forever.
// It records that the notice was shown so it never repeats. Only ever reached on
// an interactive terminal.
func promptConsent() {
	theme := themeForStdout()
	fmt.Printf("%s\n\n", theme.Heading("Anonymous telemetry is on"))
	fmt.Println("ctx-wire shares an anonymous summary like the example below, so community totals")
	fmt.Println("are real and weak filters get fixed for everyone. No commands, arguments, paths,")
	fmt.Println("output, or repo/host/user names, ever, only counts like this:")
	fmt.Println()
	fmt.Println(highlightJSON(theme, telemetry.MockPayload()))
	fmt.Println()
	fmt.Print("Keep it on? [Y/n] (Enter keeps it on): ")

	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	telemetry.MarkPreviewShown() // shown once, whatever the answer
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "n", "no":
		if err := telemetry.SetEnabled(false); err != nil {
			fmt.Printf("\n%s could not disable: %v\n\n", theme.Warn.Render("Telemetry"), err)
			return
		}
		fmt.Printf("\n%s off. Re-enable anytime: %s\n\n",
			theme.Dim.Render("Telemetry"), theme.Command.Render("ctx-wire telemetry enable"))
	default:
		fmt.Printf("\n%s staying on, thanks. Disable anytime: %s · or keep stats, drop per-command detail: %s\n\n",
			theme.OK.Render("Telemetry"), theme.Command.Render("ctx-wire telemetry disable"), theme.Command.Render("ctx-wire telemetry improvements off"))
	}
}

// consentOlive is the muted olive-green used for the consent's JSON structure
// and secondary text, in place of the flat dim gray.
var consentOlive = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a9a5b"))

// highlightJSON renders pretty-printed JSON with light syntax coloring (keys
// blue, string values green, numbers cyan, structure in muted olive), each line
// indented two spaces under the intro. Plain text when color is off.
func highlightJSON(theme ui.Theme, s string) string {
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  ")
		if !theme.Color {
			b.WriteString(line)
			continue
		}
		b.WriteString(highlightJSONLine(theme, line))
	}
	return b.String()
}

func highlightJSONLine(theme ui.Theme, line string) string {
	isNumStart := func(c byte) bool { return (c >= '0' && c <= '9') || c == '-' }
	var b strings.Builder
	i := 0
	for i < len(line) {
		// Structure run (indent, braces, colons, commas) in olive.
		start := i
		for i < len(line) && line[i] != '"' && !isNumStart(line[i]) {
			i++
		}
		if i > start {
			b.WriteString(consentOlive.Render(line[start:i]))
		}
		if i >= len(line) {
			break
		}
		if line[i] == '"' {
			j := i + 1
			for j < len(line) && line[j] != '"' {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			end := min(j+1, len(line))
			tok := line[i:end]
			k := end
			for k < len(line) && line[k] == ' ' {
				k++
			}
			if k < len(line) && line[k] == ':' {
				b.WriteString(theme.Path.Render(tok)) // key
			} else {
				b.WriteString(theme.Good.Render(tok)) // string value
			}
			i = end
			continue
		}
		// number
		j := i
		for j < len(line) && (isNumStart(line[j]) || line[j] == '.' || line[j] == 'e' || line[j] == '+') {
			j++
		}
		b.WriteString(theme.Number.Render(line[i:j]))
		i = j
	}
	return b.String()
}

// quotaFlags carries the CLI overrides for the quota view. A negative value
// means "unset on the command line" so the config default (or built-in default)
// applies.
type quotaFlags struct {
	budget int64
	window int64
}

// parseGainOptions parses gain flags into a view (one of "", history, daily,
// graph, json, csv, quota), summary options, a history limit, and quota
// overrides. View flags are mutually exclusive.
func parseGainOptions(args []string) (view string, opts gain.Options, limit int, q quotaFlags, err error) {
	q = quotaFlags{budget: -1, window: -1}
	fail := func(e error) (string, gain.Options, int, quotaFlags, error) {
		return "", gain.Options{}, 0, quotaFlags{}, e
	}
	setView := func(v string) error {
		if view != "" && view != v {
			return fmt.Errorf("--%s conflicts with --%s (choose one view)", v, view)
		}
		view = v
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--since":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--since requires a value"))
			}
			i++
			since, perr := parseSince(args[i])
			if perr != nil {
				return fail(perr)
			}
			opts.Since = since
		case strings.HasPrefix(arg, "--since="):
			since, perr := parseSince(strings.TrimPrefix(arg, "--since="))
			if perr != nil {
				return fail(perr)
			}
			opts.Since = since
		case arg == "--top":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--top requires a value"))
			}
			i++
			n, perr := strconv.Atoi(args[i])
			if perr != nil || n <= 0 {
				return fail(fmt.Errorf("invalid --top %q (want a positive integer)", args[i]))
			}
			limit = n
		case strings.HasPrefix(arg, "--top="):
			n, perr := strconv.Atoi(strings.TrimPrefix(arg, "--top="))
			if perr != nil || n <= 0 {
				return fail(fmt.Errorf("invalid --top value (want a positive integer)"))
			}
			limit = n
		case arg == "--agent":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--agent requires a name"))
			}
			i++
			opts.Agent = strings.ToLower(args[i])
		case strings.HasPrefix(arg, "--agent="):
			opts.Agent = strings.ToLower(strings.TrimPrefix(arg, "--agent="))
		case arg == "--budget", strings.HasPrefix(arg, "--budget="):
			val := strings.TrimPrefix(arg, "--budget=")
			if arg == "--budget" {
				if i+1 >= len(args) {
					return fail(fmt.Errorf("--budget requires a token count"))
				}
				i++
				val = args[i]
			}
			n, perr := parseTokenCount(val)
			if perr != nil {
				return fail(perr)
			}
			q.budget = n
			if verr := setView("quota"); verr != nil {
				return fail(verr)
			}
		case arg == "--window", strings.HasPrefix(arg, "--window="):
			val := strings.TrimPrefix(arg, "--window=")
			if arg == "--window" {
				if i+1 >= len(args) {
					return fail(fmt.Errorf("--window requires a token count"))
				}
				i++
				val = args[i]
			}
			n, perr := parseTokenCount(val)
			if perr != nil || n <= 0 {
				return fail(fmt.Errorf("invalid --window %q (want a positive token count)", val))
			}
			q.window = n
			if verr := setView("quota"); verr != nil {
				return fail(verr)
			}
		case arg == "--history", arg == "--daily", arg == "--weekly", arg == "--monthly",
			arg == "--graph", arg == "--json", arg == "--csv", arg == "--quota":
			if verr := setView(strings.TrimPrefix(arg, "--")); verr != nil {
				return fail(verr)
			}
		default:
			return fail(fmt.Errorf("unknown gain option %q", arg))
		}
	}
	// --top only affects the history view; accepting it silently elsewhere (e.g.
	// `gain --daily --top 1`) misleads, since the limit is ignored. Reject it.
	if limit > 0 && view != "history" {
		return fail(fmt.Errorf("--top requires --history"))
	}
	return view, opts, limit, q, nil
}

// parseTokenCount parses a token budget like "200000", "200k", "1.5m", or
// "2_000_000" (underscores and commas are ignored). Suffixes are case-
// insensitive: k=1e3, m=1e6. A bare negative or unparseable value is an error.
func parseTokenCount(s string) (int64, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, fmt.Errorf("empty token count")
	}
	clean := strings.NewReplacer("_", "", ",", "").Replace(raw)
	mult := 1.0
	switch {
	case strings.HasSuffix(strings.ToLower(clean), "k"):
		mult = 1_000
		clean = clean[:len(clean)-1]
	case strings.HasSuffix(strings.ToLower(clean), "m"):
		mult = 1_000_000
		clean = clean[:len(clean)-1]
	}
	f, err := strconv.ParseFloat(clean, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid token count %q (use e.g. 200000, 200k, or 1.5m)", s)
	}
	return int64(f*mult + 0.5), nil
}

// gainQuota renders the quota view: month-to-date savings against a configured
// or supplied budget, with a per-agent split. It resolves budget and context
// window from CLI overrides first, then the user config.
func gainQuota(opts gain.Options, q quotaFlags) int {
	cfg, _ := config.Load() // best-effort; a missing/broken config just means no defaults

	budget := cfg.Output.MonthlyTokenBudget
	if q.budget >= 0 {
		budget = q.budget
	}
	window := cfg.Output.ContextWindow
	if q.window > 0 {
		window = q.window
	}

	now := time.Now().UTC()
	opts.Since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	entries, err := gain.Entries(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire gain: %v\n", err)
		return 1
	}
	report := gain.Quota(entries, now.Format("2006-01"), budget, window)
	fmt.Print(gain.FormatQuotaThemed(report, themeForStdout()))
	return 0
}

func parseSince(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("--since requires a value")
	}
	if d, err := time.ParseDuration(value); err == nil {
		return time.Now().Add(-d), nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q (use a duration like 1h or an RFC3339 timestamp)", value)
}

func usageGain(out *os.File) {
	printHelp(out, helpDoc{
		usage: []string{
			"ctx-wire gain [--since <duration|RFC3339>] [--agent <name>]",
			"ctx-wire gain --history [--top N] [--agent <name>]",
			"ctx-wire gain --daily | --weekly | --monthly | --graph",
			"ctx-wire gain --json | --csv",
			"ctx-wire gain --quota [--budget <tokens>] [--window <tokens>]",
			"ctx-wire gain clear",
		},
		summary: "Show recorded token savings: totals, per-program impact, trends, and per-agent quota.",
		flags: [][2]string{
			{"--since <dur|ts>", "only savings since a duration (1h) or RFC3339 time"},
			{"--agent <name>", "only one invoking agent's commands (claude, cursor, codex, gemini, ...)"},
			{"--history", "recent commands, newest last (with agent and command; cap with --top N)"},
			{"--daily|--weekly|--monthly", "savings grouped by period"},
			{"--graph", "ASCII bar graph of daily saved bytes"},
			{"--json|--csv", "export the summary / daily breakdown"},
			{"--quota", "month-to-date savings vs a token budget, with a per-agent split"},
			{"--budget <tokens>", "monthly token budget for --quota (e.g. 200k, 1.5m)"},
			{"--window <tokens>", "context-window size for --quota framing (default 200k)"},
			{"clear", "clear local gain history for a fresh measurement window"},
		},
		examples: []string{
			"ctx-wire gain",
			"ctx-wire gain --daily",
			"ctx-wire gain --history --agent cursor",
			"ctx-wire gain --quota --budget 2m",
		},
	})
}
