package main

import (
	"fmt"
	"os"

	"ctx-wire/internal/gain"
	"ctx-wire/internal/telemetry"
)

func cmdTelemetry(args []string) int {
	if isHelpArg(args) {
		usageTelemetry(os.Stdout)
		return 0
	}
	if len(args) == 0 || args[0] == "status" || isVerboseFlag(args[0]) {
		verbose := false
		for i, a := range args {
			switch {
			case i == 0 && a == "status":
				// the explicit subcommand name; skip
			case isVerboseFlag(a):
				verbose = true
			default:
				usageHint(os.Stderr, "ctx-wire telemetry [status|preview|enable|disable]", "telemetry")
				return 2
			}
		}
		return cmdTelemetryStatus(verbose)
	}
	if len(args) != 1 {
		usageHint(os.Stderr, "ctx-wire telemetry [status|preview|enable|disable]", "telemetry")
		return 2
	}
	switch args[0] {
	case "preview":
		s, err := gain.Summarize()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry preview: %v\n", err)
			return 1
		}
		fmt.Println(telemetry.PreviewPayload(s))
		return 0
	case "enable":
		if err := telemetry.SetShareImprovements(true); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry enable: %v\n", err)
			return 1
		}
		fmt.Printf("%s command breakdown enabled (aggregate telemetry stays on)\n", themeForStdout().Success())
		return 0
	case "disable":
		if err := telemetry.SetShareImprovements(false); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry disable: %v\n", err)
			return 1
		}
		fmt.Printf("%s command breakdown disabled (aggregate telemetry stays on)\n", themeForStdout().Success())
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ctx-wire telemetry: unsupported command %q\n", args[0])
		usageHint(os.Stderr, "ctx-wire telemetry [status|preview|enable|disable]", "telemetry")
		return 2
	}
}

func isVerboseFlag(a string) bool { return a == "--verbose" || a == "-v" }

func cmdTelemetryStatus(verbose bool) int {
	status, err := telemetry.GetStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire telemetry status: %v\n", err)
		return 1
	}
	theme := themeForStdout()
	fmt.Println(theme.Heading("ctx-wire telemetry: status"))
	fmt.Println()
	fmt.Println(theme.Field("Aggregate telemetry", theme.Number.Render("on")))
	detail := "on"
	if !status.ShareImprovements {
		detail = "off"
	}
	fmt.Println(theme.Field("Command breakdown", theme.Number.Render(detail)))

	if !verbose {
		// Concise default: the answer to "what is shared?" plus how to act and
		// where to see detail. Endpoint, file paths, and the full privacy text live
		// behind --verbose so a glance stays a glance.
		fmt.Println()
		fmt.Println(theme.Dim.Render("anonymous aggregate counters stay on · `disable` drops only the per-command breakdown · `enable` restores it"))
		return 0
	}

	fmt.Println(theme.Field("Endpoint", theme.Path.Render(status.Endpoint)))
	fmt.Println(theme.Field("Config", theme.Path.Render(status.ConfigPath)))
	fmt.Println(theme.Field("State", theme.Path.Render(status.StatePath)))
	fmt.Println(theme.Field("Install reported", theme.Number.Render(fmt.Sprintf("%t", status.InstallReported))))
	fmt.Println()
	fmt.Println(theme.Dim.Render("Always sends aggregate counters: install reports, total commands, raw/emitted bytes,"))
	fmt.Println(theme.Dim.Render("bytes saved, estimated tokens saved, and per-agent/country totals."))
	fmt.Println(theme.Dim.Render("When command breakdown is on, also sends allowlisted per-program totals used to tune filters."))
	fmt.Println(theme.Dim.Render("Batches locally; flushes at most once every 30 minutes, on the first large batch, or a `ctx-wire gain` report."))
	fmt.Println(theme.Dim.Render("Never sends commands, args, paths, raw output, repo/user/host names, install IDs, or IPs."))
	return 0
}

func usageTelemetry(w *os.File) {
	printHelp(w, helpDoc{
		usage:   []string{"ctx-wire telemetry [status|preview|enable|disable]"},
		summary: "Show anonymous aggregate telemetry and toggle the per-command breakdown.",
		commands: [][2]string{
			{"status [--verbose]", "show aggregate telemetry and command-breakdown state (--verbose adds endpoint + file paths)"},
			{"preview", "print the exact anonymous payload that would be shared"},
			{"enable", "share the per-command breakdown used to improve filters"},
			{"disable", "drop the per-command breakdown; aggregate website stats stay on"},
		},
		notes: []string{
			"Telemetry never sends commands, args, paths, output, repo/user/host names, install IDs, or IPs.",
		},
	})
}
