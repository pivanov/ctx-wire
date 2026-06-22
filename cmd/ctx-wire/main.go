// Command ctx-wire filters noisy command output before it reaches an AI coding
// agent's context window.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"ctx-wire/internal/commandpolicy"
	"ctx-wire/internal/config"
	"ctx-wire/internal/filter"
	"ctx-wire/internal/hook"
	"ctx-wire/internal/install"
	"ctx-wire/internal/recent"
	"ctx-wire/internal/runner"
	"ctx-wire/internal/selfupdate"
	"ctx-wire/internal/shim"
	"ctx-wire/internal/stripstack"
	"ctx-wire/internal/telemetry"
	"ctx-wire/internal/ui"
)

// Build metadata, overridable at build time via:
//
//	go build -ldflags "-X main.version=$V -X main.commit=$C -X main.date=$D"
//
// version is also reported to MCP clients.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	// Hidden re-entry point: the detached child that performs a background
	// self-update. Handle it before any normal processing so it stays minimal and
	// can never recurse into another auto-update.
	if selfupdate.IsBackgroundArg(os.Args[1]) {
		selfupdate.RunBackground(version)
		os.Exit(0)
	}
	// Stamp the version into telemetry payloads (anonymous, opt-out) before any
	// command can trigger a send, so per-version filter-effectiveness is chartable.
	telemetry.SetBuildInfo(version)
	// Load the user config once and apply the exclude list to the shared command
	// policy (honored by both the hook rewriter and the runner). Best-effort: a
	// malformed config warns but never blocks a command.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire: %v\n", err)
	} else {
		commandpolicy.SetExcludedCommands(cfg.Hooks.ExcludeCommands)
		commandpolicy.SetFullFiles(cfg.Hooks.FullFiles)
		commandpolicy.SetTransparentPrefixes(cfg.Hooks.TransparentPrefixes)
		filter.SetUltraCompact(cfg.Output.UltraCompact)
		stripstack.SetEnabled(cfg.Output.StripStacktraces)
		if cfg.Output.Truncate != "" {
			if lvl, ok := filter.ParseTruncateLevel(cfg.Output.Truncate); ok {
				filter.SetConfiguredTruncateLevel(lvl)
			} else {
				fmt.Fprintf(os.Stderr, "ctx-wire: unknown [output] truncate level %q; using default\n", cfg.Output.Truncate)
			}
		}
		hook.SetReadCeilingMode(cfg.Hooks.ReadCeiling)
		ret := recent.Options{
			Enabled:    cfg.Retention.Enabled,
			RawBodies:  cfg.Retention.RawBodies,
			MaxEntries: cfg.Retention.MaxEntries,
		}
		// Dedup needs the recent store to compare against and to recover from, so
		// it records (at least the lean tier) even if retention was not explicitly
		// enabled. ApplyEnv runs last so the CTX_WIRE_RETENTION=0 kill switch still
		// wins: it clears retentionOpts.Enabled, and maybeDedup is gated on that,
		// so the kill switch disables dedup too.
		dedupOn := cfg.Dedup.On()
		if dedupOn {
			ret.Enabled = true
		}
		runner.SetRetention(recent.ApplyEnv(ret))
		runner.SetDedup(runner.DedupOptions{Enabled: dedupOn, Recency: cfg.Dedup.Recency()})
	}
	// Auto-update is opt-out and runs only on human-facing commands, never on the
	// run/hook/rewrite/mcp hot paths (per-command, machine-facing). It is fully
	// non-blocking: at most once per interval it spawns a detached updater and
	// returns immediately. Fail closed on a config parse error: a broken config
	// must not trigger a background download+replace the user can't see.
	if err == nil && cfg.Update.AutoEnabled() {
		switch os.Args[1] {
		case "gain", "doctor":
			selfupdate.MaybeBackgroundUpdate(version, cfg.Update.Interval())
		}
	}
	maybeRefreshManagedShims(os.Args[1])
	// Migrate existing installs: nudge hook/plugin users (once) that redundant PATH
	// shims are slowing their prompt and can be removed with one command. Advisory
	// only, never auto-deletes (see maybeAdviseRedundantShims).
	maybeAdviseRedundantShims(os.Args[1])
	switch os.Args[1] {
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "mcp":
		os.Exit(cmdMCP(os.Args[2:]))
	case "mcp-wrap":
		os.Exit(cmdMCPWrap(os.Args[2:]))
	case "hook":
		os.Exit(cmdHook(os.Args[2:]))
	case "rewrite":
		os.Exit(cmdRewrite(os.Args[2:]))
	case "init":
		os.Exit(cmdInit(os.Args[2:]))
	case "shims":
		os.Exit(cmdShims(os.Args[2:]))
	case "uninstall":
		os.Exit(cmdUninstall(os.Args[2:]))
	case "update":
		os.Exit(cmdUpdate(os.Args[2:]))
	case "trust":
		os.Exit(cmdTrust(os.Args[2:]))
	case "untrust":
		os.Exit(cmdUntrust(os.Args[2:]))
	case "gain":
		os.Exit(cmdGain(os.Args[2:]))
	case "explain":
		os.Exit(cmdExplain(os.Args[2:]))
	case "fetch":
		os.Exit(cmdFetch(os.Args[2:]))
	case "inspect":
		os.Exit(cmdInspect(os.Args[2:]))
	case "tune":
		os.Exit(cmdTune(os.Args[2:]))
	case "filters":
		os.Exit(cmdFilters(os.Args[2:]))
	case "telemetry":
		os.Exit(cmdTelemetry(os.Args[2:]))
	case "discover":
		os.Exit(cmdDiscover(os.Args[2:]))
	case "learn":
		os.Exit(cmdLearn(os.Args[2:]))
	case "session":
		os.Exit(cmdSession(os.Args[2:]))
	case "doctor":
		os.Exit(cmdDoctor(os.Args[2:]))
	case "verify":
		os.Exit(cmdVerify(os.Args[2:]))
	case "version", "--version":
		os.Exit(cmdVersion())
	case "-h", "--help", "help":
		usage(os.Stdout)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "ctx-wire: unknown command %q\n", os.Args[1])
		if suggestion := suggestCommand(os.Args[1]); suggestion != "" {
			fmt.Fprintf(os.Stderr, "did you mean %q?\n", suggestion)
		}
		fmt.Fprintln(os.Stderr, "")
		usage(os.Stderr)
		os.Exit(2)
	}
}

func maybeRefreshManagedShims(subcommand string) {
	switch subcommand {
	case "doctor", "gain", "update", "version":
	default:
		return
	}
	ctxWire, ok := stableCurrentBinaryPath()
	if !ok {
		return
	}
	_ = shim.RefreshManaged(ctxWire)
}

func stableCurrentBinaryPath() (string, bool) {
	if version == "" || version == "dev" {
		return "", false
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if found, err := exec.LookPath("ctx-wire"); err == nil && sameExecutablePath(found, exe) {
		return found, true
	}
	if dest, err := install.SelfInstallPath(); err == nil && sameExecutablePath(dest, exe) {
		return dest, true
	}
	return "", false
}

var knownCommands = []string{
	"run", "mcp", "hook", "rewrite", "init", "shims", "trust", "untrust", "gain", "explain", "fetch",
	"uninstall", "update", "tune", "telemetry", "discover", "learn", "session", "doctor", "verify", "version", "help",
}

func suggestCommand(input string) string {
	best := ""
	bestDist := 3
	for _, cmd := range knownCommands {
		d := editDistance(input, cmd)
		if d < bestDist {
			bestDist = d
			best = cmd
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func usage(out *os.File) {
	theme := themeForFile(out)
	fmt.Fprintf(out, "%s %s\n\n", theme.Label.Render("usage:"), theme.Command.Render("ctx-wire <command> [args]"))
	usageSection(out, theme, "daily", []usageRow{
		{"init <target>", "install ctx-wire and wire an agent (shims only for steering-only agents)"},
		{"doctor", "check binary, hooks, shims, storage, filters, and telemetry"},
		{"gain", "show saved bytes/tokens and per-program impact"},
		{"tune", "report filter gaps; preview, bundle, or open a sanitized issue"},
		{"telemetry", "show or change anonymous aggregate telemetry status"},
	})
	fmt.Fprintln(out)
	usageSection(out, theme, "diagnose", []usageRow{
		{"explain <cmd>", "diagnose one command's rewrite/filter decision (read-only)"},
		{"fetch <hash>", "recover full scrubbed output for a truncated/failed command"},
		{"inspect [n]", "show raw-vs-filtered for a recent command (needs [retention])"},
		{"discover", "find agent commands that escaped ctx-wire (read-only, local logs)"},
		{"learn", "mine transcripts for failed->corrected commands into rule hints"},
		{"session", "per-session ctx-wire adoption across agent transcripts (read-only)"},
		{"verify [filter]", "run built-in filter conformance tests"},
		{"trust", "approve this project's .ctx-wire/filters.toml"},
		{"untrust", "revoke trust for this project's .ctx-wire/filters.toml"},
		{"filters pull|publish", "share filters: pull (verified, untrusted) or publish a local one"},
	})
	fmt.Fprintln(out)
	usageSection(out, theme, "manage", []usageRow{
		{"run <cmd> [args]", "manual wrapper used by hooks/shims to filter command output"},
		{"rewrite <line>", "debug the shell rewrite that hooks would apply"},
		{"update [--check]", "upgrade ctx-wire to the latest release"},
		{"shims [status|install|uninstall]", "manage the optional default PATH shims"},
		{"uninstall", "remove ctx-wire binary, shims, and ctx-wire hook entries"},
		{"version", "print version and build metadata"},
	})
	// hook and mcp are deliberately omitted: they are invoked by agents and MCP
	// clients (wired up by `ctx-wire init <agent>`), never typed by a person. They still
	// exist and respond to `<cmd> --help`.
}

type usageRow struct {
	command string
	help    string
}

func usageSection(out *os.File, theme ui.Theme, title string, rows []usageRow) {
	fmt.Fprintln(out, theme.Section.Render(title+":"))
	for _, row := range rows {
		fmt.Fprintf(out, "  %s %s\n", theme.Command.Render(fmt.Sprintf("%-18s", row.command)), row.help)
	}
}

func isHelpArg(args []string) bool {
	return len(args) > 0 && (args[0] == "-h" || args[0] == "--help")
}

func usageLine(w io.Writer, line string) {
	theme := themeFor(w)
	fmt.Fprintf(w, "%s %s\n", theme.Label.Render("usage:"), theme.Command.Render(line))
}

// cmdVersion prints the version and build metadata.
func cmdVersion() int {
	fmt.Printf("ctx-wire %s (commit %s, built %s)\n", version, commit, date)
	return 0
}
