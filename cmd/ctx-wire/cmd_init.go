package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/install"
	"ctx-wire/internal/shim"
	"ctx-wire/internal/telemetry"
	"ctx-wire/internal/ui"
)

// cmdInit installs ctx-wire locally or into an agent's configuration.
func cmdInit(args []string) int {
	if isHelpArg(args) {
		agentList := strings.Join(install.AgentNames(), " · ")
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire init <agent> [--no-mcp]"},
			summary: "Wire an agent: install the binary into ~/.local/bin and configure that agent's hooks/rules.",
			flags: [][2]string{
				{"--no-mcp", "skip MCP auto-wrap (init claude otherwise relays known snapshot-heavy servers through mcp-wrap --compress)"},
			},
			examples: []string{
				"ctx-wire init claude    # wire Claude Code",
				"ctx-wire init codex     # wire Codex",
			},
			notes: []string{
				"a target agent is required:\n    " + agentList,
				"hook/plugin-capable agents (claude, codex, cursor, gemini, copilot, opencode, pi, hermes) are covered by their hook/plugin, so `init` no longer installs PATH shims for them. Steering-only agents (cline, windsurf, kilocode, antigravity, vscode, visualstudio) still get shims. Manage shims explicitly with `ctx-wire shims install|uninstall|status`.",
			},
		})
		return 0
	}
	if len(args) == 0 {
		return initMissingTarget()
	}
	noMCP := false
	kept := args[:0:0]
	for _, a := range args {
		switch a {
		case "--no-mcp":
			noMCP = true
		default:
			kept = append(kept, a)
		}
	}
	args = kept
	if len(args) == 0 {
		return initMissingTarget()
	}
	agent := args[0]
	if agent == "codex" {
		return cmdInitCodex()
	}
	if agent == "gemini" {
		return cmdInitGemini()
	}

	// claude: wire every detected config dir and return early so the single-path
	// tail below does not run for claude.
	if agent == "claude" {
		theme := themeForStdout()
		if code := initClaude(theme, noMCP); code != 0 {
			return code
		}
		if code := installSelfAndShims(theme, "claude"); code != 0 {
			return code
		}
		return 0
	}

	// Every remaining agent installs uniformly: resolve its path, write its
	// wiring, report Configured/OK. That per-agent logic lives once in the
	// install registry (install.InstallAgent), so this layer only renders the
	// shared message and lands the binary/shims. canonicalInitAgent collapses
	// aliases (vs, github-copilot) to the registry's canonical names. os.Getwd is
	// passed lazily: it runs only for workdir-scoped agents, so an unknown name
	// reports "unsupported" and home-scoped agents wire even without a cwd.
	canonical := canonicalInitAgent(agent)
	path, changed, ok, err := install.InstallAgent(canonical, os.Getwd)
	if !ok {
		fmt.Fprintf(os.Stderr, "ctx-wire init: unsupported agent %q (supported: %s)\n", agent, strings.Join(install.AgentNames(), ", "))
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	theme := themeForStdout()
	if changed {
		fmt.Printf("%s ctx-wire in %s\n", theme.OK.Render("Configured"), theme.Path.Render(path))
	} else {
		fmt.Printf("%s ctx-wire already configured in %s\n", theme.OK.Render("OK"), theme.Path.Render(path))
	}
	if canonical == "visualstudio" {
		fmt.Printf("   %s\n", theme.Dim.Render("Visual Studio still needs the ctx-wire MCP server and its read_file/run_command tools enabled in its Copilot settings before the agent can use them. For a managed fleet, see deploy/windows/enable-ctx-wire-visualstudio.ps1."))
	}
	if code := installSelfAndShims(theme, canonical); code != 0 {
		return code
	}
	return 0
}

// initClaude wires every detected Claude config directory. For each dir it
// installs the Bash hook (settings.json), the CLAUDE.md instructions, and
// the per-config MCP auto-wrap. installSelfAndShims runs separately (once,
// global). Best-effort per dir, never silent.
func initClaude(theme ui.Theme, noMCP bool) int {
	dirs, err := install.ClaudeConfigDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: cannot detect claude config dirs: %v\n", err)
		return 1
	}
	for _, dir := range dirs {
		settingsPath := filepath.Join(dir, "settings.json")
		memPath := filepath.Join(dir, "CLAUDE.md")

		// Bash hook.
		changed, herr := install.InstallClaude(settingsPath)
		if herr != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire init: %s hook: %v\n", dir, herr)
			continue // best-effort: skip the rest for this dir on error
		}
		if changed {
			fmt.Printf("%s ctx-wire in %s\n", theme.OK.Render("Configured"), theme.Path.Render(settingsPath))
		} else {
			fmt.Printf("%s ctx-wire already configured in %s\n", theme.OK.Render("OK"), theme.Path.Render(settingsPath))
		}

		// CLAUDE.md memory.
		memChanged, merr := install.InstallClaudeMemory(memPath)
		if merr != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire init: %s memory: %v\n", dir, merr)
		} else if memChanged {
			fmt.Printf("%s ctx-wire instructions in %s\n", theme.OK.Render("Configured"), theme.Path.Render(memPath))
		} else {
			fmt.Printf("%s ctx-wire instructions already in %s\n", theme.OK.Render("OK"), theme.Path.Render(memPath))
		}

		// MCP auto-wrap for this config dir's MCP config.
		if !noMCP {
			initAutoWrapMCPForDir(theme, dir)
		}

		// Read-ceiling PostToolUse feature: on by default, no flag. Large unranged
		// native Reads are scrubbed + reshaped to head+tail with a recoverable
		// `ctx-wire fetch <hash>` handle. Disable per machine with
		// `read_ceiling = "off"` in config or CTX_WIRE_READ_CEILING=off.
		if changed, rcErr := install.InstallClaudeReadCeiling(settingsPath); rcErr != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire init: read-ceiling matcher: %v\n", rcErr)
		} else if changed {
			fmt.Printf("%s native-Read ceiling on (large reads reshaped to head+tail, recoverable via `ctx-wire fetch`)\n", theme.OK.Render("Configured"))
		}
	}
	return 0
}

// claudeMCPConfigPath returns the .claude.json that holds MCP server entries for
// a Claude config dir. The location differs by dir, verified on disk:
//   - default ~/.claude: the real config is the SIBLING ~/.claude.json (its
//     in-dir ~/.claude/.claude.json is an empty stub), so resolve it via
//     defaultMCPConfigPath().
//   - a custom CLAUDE_CONFIG_DIR (e.g. ~/.claude-main): the file lives INSIDE
//     the dir, <dir>/.claude.json.
//
// A naive `configDir + ".json"` wraps the empty in-dir stub for the default and
// a non-existent sibling for custom dirs, missing every real custom config.
func claudeMCPConfigPath(configDir string) string {
	if home, err := os.UserHomeDir(); err == nil &&
		filepath.Clean(configDir) == filepath.Join(home, ".claude") {
		return defaultMCPConfigPath()
	}
	return filepath.Join(configDir, ".claude.json")
}

// initAutoWrapMCPForDir turns on snapshot compression for known snapshot-heavy
// MCP servers in the given config dir's MCP config. It is the per-config-dir
// variant of the old initAutoWrapMCP. Best-effort, never fails init.
func initAutoWrapMCPForDir(theme ui.Theme, configDir string) {
	mcpCfg := claudeMCPConfigPath(configDir)
	wrapped, err := autoWrapSnapshotMCP(mcpCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: mcp auto-wrap for %s skipped: %v\n", configDir, err)
		return
	}
	if len(wrapped) == 0 {
		return
	}
	for _, name := range wrapped {
		fmt.Printf("%s MCP server %q now relays through `ctx-wire mcp-wrap --compress` (browser snapshots reduced, raw spooled locally)\n",
			theme.OK.Render("Configured"), name)
	}
	fmt.Printf("   %s\n", theme.Dim.Render("restart Claude to apply · revert anytime: ctx-wire mcp-wrap uninstall <server> (or skip with init --no-mcp)"))
}

// canonicalInitAgent maps an init target to the agent name used for per-agent
// install telemetry, collapsing aliases so the breakdown does not split.
func canonicalInitAgent(target string) string {
	switch target {
	case "vs", "visualstudio":
		return "visualstudio"
	case "copilot", "github-copilot":
		return "copilot"
	default:
		return target
	}
}

// initMissingTarget handles `ctx-wire init` with no agent. A target is required
// so users always know which agent got wired; `init <agent>` installs the binary
// and shims as part of configuring that agent.
func initMissingTarget() int {
	theme := themeForFile(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s `ctx-wire init` needs an agent.\n\n", theme.Warn.Render("missing agent"))
	fmt.Fprintln(os.Stderr, "Wire an agent (installs the binary and that agent's hooks; steering-only agents also get PATH shims):")
	fmt.Fprintf(os.Stderr, "  %s\n", theme.Command.Render("ctx-wire init claude"))
	fmt.Fprintln(os.Stderr, theme.Dim.Render("  "+strings.Join(install.AgentNames(), " · ")))
	return 2
}

func cmdInitGemini() int {
	hookPath, err := install.GeminiHookPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	settingsPath, err := install.GeminiSettingsPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}

	hookChanged, err := install.InstallGeminiHook(hookPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	settingsChanged, err := install.InstallGeminiSettings(settingsPath, hookPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}

	theme := themeForStdout()
	if hookChanged {
		fmt.Printf("%s Gemini hook wrapper at %s\n", theme.OK.Render("Installed"), theme.Path.Render(hookPath))
	} else {
		fmt.Printf("%s Gemini hook wrapper already present at %s\n", theme.OK.Render("OK"), theme.Path.Render(hookPath))
	}
	if settingsChanged {
		fmt.Printf("%s Gemini hook into %s\n", theme.OK.Render("Configured"), theme.Path.Render(settingsPath))
	} else {
		fmt.Printf("%s Gemini hook already present in %s\n", theme.OK.Render("OK"), theme.Path.Render(settingsPath))
	}
	installInstructions(install.GeminiMemoryPath, install.InstallGeminiMemory)
	if code := installSelfAndShims(theme, "gemini"); code != 0 {
		return code
	}
	return 0
}

// installSelfAndShims installs the running ctx-wire binary into ~/.local/bin and,
// for steering-only agents, adds managed PATH shims. Every `init <agent>` runs
// this first, so wiring an agent always lands the binary; shims are added only
// when the agent is not hook/plugin-capable (see the IsHookCapable gate below),
// since a hook/plugin already covers model-visible commands. agentName attributes
// the install per agent.
func installSelfAndShims(theme ui.Theme, agentName string) int {
	source, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	dest, err := install.SelfInstallPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	changed, err := install.InstallSelf(source, dest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}

	if changed {
		fmt.Printf("%s ctx-wire to %s\n", theme.OK.Render("Installed"), theme.Path.Render(dest))
	} else {
		fmt.Printf("%s ctx-wire already installed at %s\n", theme.OK.Render("OK"), theme.Path.Render(dest))
	}
	if found, err := exec.LookPath("ctx-wire"); err != nil {
		fmt.Printf("%s add %s to PATH so agents can find ctx-wire\n", theme.Warn.Render("PATH"), theme.Path.Render(filepath.Dir(dest)))
	} else if !sameExecutablePath(found, dest) {
		fmt.Printf("%s PATH resolves ctx-wire to %s, not %s\n", theme.Warn.Render("PATH"), theme.Path.Render(found), theme.Path.Render(dest))
	}
	// Default PATH shims are installed only for steering-only agents. Hook- and
	// plugin-capable agents already rewrite model-visible commands through their
	// hook/plugin, so default shims add no coverage there; and when the shim dir is
	// early on PATH they cost ~15ms per shimmed command on every shell prompt
	// render. Such agents can still opt in explicitly with `ctx-wire shims install`.
	if agent.IsHookCapable(agentName) {
		fmt.Printf("%s\n", theme.Dim.Render(fmt.Sprintf(
			"note: %s rewrites commands through its hook/plugin, so PATH shims were not installed (they add no coverage here and can slow shell startup). Add them anytime with `ctx-wire shims install`.",
			agentName)))
	} else if code := installShims(dest, theme); code != 0 {
		return code
	}
	reportInstallTelemetry(theme, agentName)
	return 0
}

func reportInstallTelemetry(theme ui.Theme, agentName string) {
	result, err := telemetry.ReportInstall(agentName)
	if err != nil {
		fmt.Printf("%s telemetry install report not sent: %v\n", theme.Warn.Render("Telemetry"), err)
		return
	}
	// Show the telemetry notice the first time this local config reports an
	// install. Aggregate telemetry is on, so this is the disclosure point.
	// Repeated `init <agent>` runs still send install events, but stay quiet.
	if result.MachineFirst {
		fmt.Printf("%s anonymous aggregate telemetry is on (drop command breakdown: ctx-wire telemetry disable)\n", theme.OK.Render("Telemetry"))
	}
}

func installShims(ctxWirePath string, theme ui.Theme) int {
	dir := filepath.Dir(ctxWirePath)
	report, err := shim.InstallDefault(dir, ctxWirePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	// Installing shims here is always deliberate (a steering agent's init or an
	// explicit `ctx-wire shims install`), so record that intent: advisory code must
	// never nudge the user to remove shims they asked for.
	shim.MarkKeep(report.Dir)
	clearNudgeMarker(report.Dir) // shim set changed: reset the once-only advisory state
	total := len(report.Commands)
	installed := len(report.Changed) + len(report.Unchanged)
	if len(report.Changed) > 0 {
		fmt.Printf("%s %d/%d command shims in %s\n", theme.OK.Render("Installed"), installed, total, theme.Path.Render(report.Dir))
	} else {
		fmt.Printf("%s %d/%d command shims already present in %s\n", theme.OK.Render("OK"), installed, total, theme.Path.Render(report.Dir))
	}
	if len(report.Skipped) > 0 {
		fmt.Printf("%s skipped existing non-ctx-wire files: %s\n", theme.Warn.Render("Shims"), strings.Join(report.Skipped, ", "))
	}
	if len(report.Missing) > 0 {
		// Not a problem: ctx-wire only shims tools that exist, so an uninstalled
		// tool is simply skipped (shimming it would make it look installed). Show
		// which ones, dimmed, so it reads as a benign note rather than a warning.
		fmt.Printf("%s\n", theme.Dim.Render(fmt.Sprintf(
			"note: %d tool(s) not installed here, so not shimmed: %s (re-run `ctx-wire init <agent>` after installing any)",
			len(report.Missing), strings.Join(report.Missing, ", "))))
	}
	return 0
}

func sameExecutablePath(a, b string) bool {
	a = cleanExecutablePath(a)
	b = cleanExecutablePath(b)
	return a == b
}

func cleanExecutablePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

// cmdInitCodex installs the Codex hook and surfaces the two user-gated steps
// (enabling the hooks feature and granting trust) without performing them, so
// ctx-wire never bypasses Codex's trust model.
func cmdInitCodex() int {
	hooksPath, err := install.CodexHooksPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	changed, err := install.InstallCodexHooks(hooksPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	theme := themeForStdout()
	if changed {
		fmt.Printf("%s ctx-wire hook into %s\n", theme.OK.Render("Installed"), theme.Path.Render(hooksPath))
	} else {
		fmt.Printf("%s ctx-wire hook already present in %s\n", theme.OK.Render("OK"), theme.Path.Render(hooksPath))
	}

	// State the permission posture plainly. ctx-wire is a filter, not a gate: by
	// default it auto-approves the commands it wraps so codex runs uninterrupted
	// (the point, for autonomous work). That also means it does NOT prompt before
	// destructive wrapped commands, safety stays with codex's own approval policy.
	fmt.Printf("\n%s ctx-wire auto-approves the commands it wraps so codex is not\n", theme.Warn.Render("Permissions:"))
	fmt.Println("   double-prompted. It is a filter, not a permission boundary: it does")
	fmt.Println("   not prompt before wrapped commands (including destructive ones),")
	fmt.Println("   so safety stays with codex's own approval policy.")
	fmt.Printf("   Want a ctx-wire guard that prompts on anything but read/build/test?\n")
	fmt.Printf("   Set %s in codex's shell env.\n", theme.Path.Render("CTX_WIRE_CODEX_SAFE=1"))

	configPath, err := install.CodexConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}
	enabled, err := install.CodexHooksEnabled(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire init: %v\n", err)
		return 1
	}

	// Agent attribution: Codex's sandbox can block the `ps` process-tree walk,
	// which leaves direct `ctx-wire run` commands unattributed in gain. Setting
	// CTX_WIRE_AGENT via Codex's shell env policy fixes that. This is the one
	// deliberate write ctx-wire makes to config.toml: it only labels ctx-wire
	// telemetry, it is not a hooks or trust change (those stay user-owned).
	// Best-effort: a failure here never fails init.
	switch res, aerr := install.InstallCodexAgentEnv(configPath); {
	case aerr != nil:
		fmt.Fprintf(os.Stderr, "ctx-wire init: codex agent attribution: %v\n", aerr)
	case res == install.CodexEnvUpdated:
		fmt.Printf("%s CTX_WIRE_AGENT=codex in %s (shell_environment_policy.set)\n", theme.OK.Render("Configured"), theme.Path.Render(configPath))
		fmt.Println("   Labels ctx-wire telemetry when the sandbox blocks ps; not a hooks or trust change.")
		fmt.Println("   Applies on the next Codex session; `ctx-wire uninstall` reverts this key.")
	case res == install.CodexEnvNoChange:
		fmt.Printf("%s agent attribution already configured in %s\n", theme.OK.Render("OK"), theme.Path.Render(configPath))
	case res == install.CodexEnvUserManaged:
		fmt.Printf("%s %s already set to a custom value in %s; leaving it as is\n", theme.OK.Render("OK"), install.CodexAgentEnvKey, theme.Path.Render(configPath))
	case res == install.CodexEnvManual:
		fmt.Printf("%s could not confidently edit %s; for agent attribution add manually:\n", theme.Warn.Render("Note"), theme.Path.Render(configPath))
		for _, line := range strings.Split(install.CodexAgentEnvSnippet, "\n") {
			fmt.Printf("       %s\n", line)
		}
	}

	// Gain-log durability: Codex's workspace-write sandbox denies writes to
	// ctx-wire's data dir, so the gain log silently falls back to a $TMPDIR copy
	// macOS eventually purges (idle janitor / OS update). Granting write to that
	// one directory keeps codex's local gain history durable. Best-effort.
	if root, rerr := install.CodexWritableRoot(); rerr == nil {
		switch res, serr := install.InstallCodexWritableRoot(configPath, root); {
		case serr != nil:
			fmt.Fprintf(os.Stderr, "ctx-wire init: codex gain durability: %v\n", serr)
		case res == install.CodexSandboxUpdated:
			fmt.Printf("%s %s in %s (sandbox_workspace_write.writable_roots)\n", theme.OK.Render("Configured"), theme.Path.Render(root), theme.Path.Render(configPath))
			fmt.Println("   Keeps codex's gain log durable under the sandbox; grants write to ctx-wire's data dir only.")
			fmt.Println("   Applies on the next Codex session; `ctx-wire uninstall` reverts this root.")
		case res == install.CodexSandboxNoChange:
			fmt.Printf("%s codex gain durability already configured in %s\n", theme.OK.Render("OK"), theme.Path.Render(configPath))
		case res == install.CodexSandboxConflict:
			fmt.Printf("%s %s uses Codex's newer [permissions] system; leaving the sandbox config untouched.\n", theme.Warn.Render("Note"), theme.Path.Render(configPath))
			fmt.Printf("       For durable codex gain, grant write access to %s in your permissions config.\n", theme.Path.Render(root))
		case res == install.CodexSandboxManual:
			fmt.Printf("%s could not confidently edit %s; for durable codex gain add manually:\n", theme.Warn.Render("Note"), theme.Path.Render(configPath))
			for _, line := range strings.Split(install.CodexWritableRootSnippet(root), "\n") {
				fmt.Printf("       %s\n", line)
			}
		}
	}

	fmt.Printf("\n%s\n", theme.Section.Render("Two user steps remain (ctx-wire does not perform them for you):"))
	if !enabled {
		fmt.Printf("  1. Enable hooks: add to %s\n", theme.Path.Render(configPath))
		fmt.Println("       [features]")
		fmt.Println("       hooks = true")
	} else {
		fmt.Printf("  1. %s\n", theme.OK.Render("Hooks feature already enabled."))
	}
	fmt.Println("  2. Trust the hook: run codex, open `/hooks`, review the ctx-wire")
	fmt.Println("     entry, and trust it. Codex re-prompts if the hook changes.")
	installInstructions(install.CodexAgentsPath, install.InstallCodexAgents)
	if code := installSelfAndShims(theme, "codex"); code != 0 {
		return code
	}
	return 0
}

// installInstructions writes the ctx-wire instruction block (which steers the
// agent toward shell reads over the built-in Read/Grep/Glob tools) and reports
// it. Best-effort: a failure here never fails init, since the hook is the
// critical piece.
func installInstructions(pathFn func() (string, error), do func(string) (bool, error)) {
	p, err := pathFn()
	if err != nil {
		return
	}
	changed, err := do(p)
	if err != nil {
		return
	}
	theme := themeForStdout()
	if changed {
		fmt.Printf("%s ctx-wire instructions in %s\n", theme.OK.Render("Configured"), theme.Path.Render(p))
	} else {
		fmt.Printf("%s ctx-wire instructions already in %s\n", theme.OK.Render("OK"), theme.Path.Render(p))
	}
}
