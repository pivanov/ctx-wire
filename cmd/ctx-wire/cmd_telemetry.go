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
				usageHint(os.Stderr, "ctx-wire telemetry [status|preview|enable|disable|improvements|forget]", "telemetry")
				return 2
			}
		}
		return cmdTelemetryStatus(verbose)
	}
	if args[0] == "improvements" {
		return cmdTelemetryImprovements(args[1:])
	}
	if len(args) != 1 {
		usageHint(os.Stderr, "ctx-wire telemetry [status|preview|enable|disable|forget|improvements]", "telemetry")
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
		if err := telemetry.SetEnabled(true); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry enable: %v\n", err)
			return 1
		}
		fmt.Printf("%s telemetry enabled\n", themeForStdout().Success())
		return 0
	case "disable":
		if err := telemetry.SetEnabled(false); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry disable: %v\n", err)
			return 1
		}
		fmt.Printf("%s telemetry disabled\n", themeForStdout().Success())
		return 0
	case "forget":
		if err := telemetry.Forget(); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry forget: %v\n", err)
			return 1
		}
		fmt.Printf("%s consent withdrawn; local telemetry data erased (stays off)\n", themeForStdout().Success())
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ctx-wire telemetry: unsupported command %q\n", args[0])
		usageHint(os.Stderr, "ctx-wire telemetry [status|preview|enable|disable|improvements|forget]", "telemetry")
		return 2
	}
}

func cmdTelemetryImprovements(args []string) int {
	theme := themeForStdout()
	if len(args) == 0 {
		status, err := telemetry.GetStatus()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry improvements: %v\n", err)
			return 1
		}
		state := "on"
		if !status.ShareImprovements {
			state = "off"
		}
		fmt.Println(theme.Field("Improvement data", theme.Number.Render(state)))
		fmt.Println(theme.Dim.Render("per-command breakdown used to improve filters · change: `ctx-wire telemetry improvements on|off`"))
		return 0
	}
	if len(args) != 1 {
		usageHint(os.Stderr, "ctx-wire telemetry improvements [on|off]", "telemetry")
		return 2
	}
	switch args[0] {
	case "on", "enable":
		if err := telemetry.SetShareImprovements(true); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry improvements on: %v\n", err)
			return 1
		}
		fmt.Printf("%s improvement data on (per-command breakdown will be shared)\n", theme.Success())
		return 0
	case "off", "disable":
		if err := telemetry.SetShareImprovements(false); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire telemetry improvements off: %v\n", err)
			return 1
		}
		fmt.Printf("%s improvement data off (per-command breakdown no longer shared; aggregate stats unaffected)\n", theme.Success())
		return 0
	default:
		usageHint(os.Stderr, "ctx-wire telemetry improvements [on|off]", "telemetry")
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
	state := "enabled"
	if !status.Enabled {
		state = "disabled"
	}
	if status.ForcedByEnv {
		state += " (from CTX_WIRE_TELEMETRY)"
	}
	fmt.Println(theme.Heading("ctx-wire telemetry: status"))
	fmt.Println()
	fmt.Println(theme.Field("Telemetry", theme.Number.Render(state)))
	if status.Enabled {
		impr := "on"
		if !status.ShareImprovements {
			impr = "off"
		}
		fmt.Println(theme.Field("Improvement data", theme.Number.Render(impr)))
	}

	if !verbose {
		// Concise default: the answer to "is it on?" plus how to act and where to
		// see detail. Endpoint, file paths, and the full privacy text live behind
		// --verbose so a glance stays a glance.
		fmt.Println()
		if status.Enabled {
			fmt.Println(theme.Dim.Render("anonymous aggregate counters, on by default · disable all: `ctx-wire telemetry disable` · keep stats, drop per-command detail: `ctx-wire telemetry improvements off`"))
		} else {
			fmt.Println(theme.Dim.Render("re-enable: `ctx-wire telemetry enable` · details: `ctx-wire telemetry status --verbose`"))
		}
		return 0
	}

	fmt.Println(theme.Field("Endpoint", theme.Path.Render(status.Endpoint)))
	fmt.Println(theme.Field("Config", theme.Path.Render(status.ConfigPath)))
	fmt.Println(theme.Field("State", theme.Path.Render(status.StatePath)))
	fmt.Println(theme.Field("Install reported", theme.Number.Render(fmt.Sprintf("%t", status.InstallReported))))
	fmt.Println()
	fmt.Println(theme.Dim.Render("Sends only aggregate counters: install reports, total commands, raw/emitted bytes,"))
	fmt.Println(theme.Dim.Render("bytes saved, estimated tokens saved, and per-program + per-agent totals."))
	fmt.Println(theme.Dim.Render("Batches locally; flushes at most once every 30 minutes, on the first large batch, or a `ctx-wire gain` report."))
	fmt.Println(theme.Dim.Render("Never sends commands, args, paths, raw output, repo/user/host names, install IDs, or IPs."))
	return 0
}

func usageTelemetry(w *os.File) {
	printHelp(w, helpDoc{
		usage:   []string{"ctx-wire telemetry [status|preview|enable|disable|forget|improvements]"},
		summary: "Show or change anonymous, aggregate, token-only telemetry (opt-out: on by default; disable any time).",
		commands: [][2]string{
			{"status [--verbose]", "show whether telemetry is on (--verbose adds endpoint + file paths)"},
			{"preview", "print the exact anonymous payload that would be shared"},
			{"enable", "turn telemetry on"},
			{"disable", "turn all telemetry off"},
			{"improvements [on|off]", "toggle only the per-command breakdown used to improve filters (stats stay on)"},
			{"forget", "withdraw consent and erase local data (stays off, never re-enabled by an update)"},
		},
		notes: []string{
			"Set CTX_WIRE_TELEMETRY=0 to disable all telemetry for a single process.",
			"Set CTX_WIRE_TELEMETRY_IMPROVEMENTS=0 to drop only the per-command breakdown.",
		},
	})
}
