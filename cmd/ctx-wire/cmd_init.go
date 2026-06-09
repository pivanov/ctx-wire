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
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire init <agent>"},
			summary: "Wire an agent: install the binary into ~/.local/bin and configure that agent's hooks/rules.",
			examples: []string{
				"ctx-wire init claude    # wire Claude Code",
				"ctx-wire init codex     # wire Codex",
			},
			notes: []string{
				"a target agent is required:\n    claude · codex · cursor · gemini · copilot · cline · windsurf · kilocode · antigravity · opencode · pi · hermes · vscode · visualstudio",
				"hook/plugin-capable agents (claude, codex, cursor, gemini, copilot, opencode, pi, hermes) are covered by their hook/plugin, so `init` no longer installs PATH shims for them. Steering-only agents (cline, windsurf, kilocode, antigravity, vscode, visualstudio) still get shims. Manage shims explicitly with `ctx-wire shims install|uninstall|status`.",
			},
		})
		return 0
	}
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

	var path string
	var changed bool
	var err error
	switch agent {
	case "claude":
		if path, err = install.ClaudeSettingsPath(); err == nil {
			changed, err = install.InstallClaude(path)
		}
		if err == nil {
			installInstructions(install.ClaudeMemoryPath, install.InstallClaudeMemory)
		}
	case "cursor":
		if path, err = install.CursorHooksPath(); err == nil {
			changed, err = install.InstallCursor(path)
		}
	case "vscode":
		var wd string
		if wd, err = os.Getwd(); err == nil {
			path = install.VSCodeMCPPath(wd)
			changed, err = install.InstallMCP(path, "vscode")
		}
	case "visualstudio", "vs":
		if path, err = install.VisualStudioMCPPath(); err == nil {
			changed, err = install.InstallMCP(path, "visualstudio")
		}
	case "cline":
		var wd string
		if wd, err = os.Getwd(); err == nil {
			path = install.ClineRulesPath(wd)
			changed, err = install.InstallCline(path)
		}
	case "windsurf":
		var wd string
		if wd, err = os.Getwd(); err == nil {
			path = install.WindsurfRulesPath(wd)
			changed, err = install.InstallWindsurf(path)
		}
	case "kilocode":
		var wd string
		if wd, err = os.Getwd(); err == nil {
			path = install.KilocodeRulesPath(wd)
			changed, err = install.InstallKilocode(path)
		}
	case "antigravity":
		var wd string
		if wd, err = os.Getwd(); err == nil {
			path = install.AntigravityRulesPath(wd)
			changed, err = install.InstallAntigravity(path)
		}
	case "opencode":
		if path, err = install.OpenCodePluginPath(); err == nil {
			changed, err = install.InstallOpenCode(path)
		}
	case "pi":
		if path, err = install.PiPluginPath(); err == nil {
			changed, err = install.InstallPi(path)
		}
	case "hermes":
		if path, err = install.HermesPluginDir(); err == nil {
			changed, err = install.InstallHermes(path)
		}
	case "copilot", "github-copilot":
		var wd string
		if wd, err = os.Getwd(); err == nil {
			path = filepath.Join(wd, ".github")
			changed, err = install.InstallCopilot(install.CopilotInstructionsPath(wd), install.CopilotHookPath(wd))
		}
	default:
		fmt.Fprintf(os.Stderr, "ctx-wire init: unsupported agent %q (supported: claude, cursor, codex, gemini, cline, windsurf, kilocode, antigravity, opencode, pi, hermes, copilot, vscode, visualstudio)\n", agent)
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
	if code := installSelfAndShims(theme, canonicalInitAgent(agent)); code != 0 {
		return code
	}
	return 0
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
	fmt.Fprintln(os.Stderr, theme.Dim.Render("  claude · codex · cursor · gemini · copilot · cline · windsurf · kilocode · antigravity · opencode · pi · hermes · vscode · visualstudio"))
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

// installSelfAndShims installs the running ctx-wire binary into ~/.local/bin and
// adds managed shims. Every `init <agent>` runs this first, so wiring an agent
// always lands the binary and shims; agentName attributes the install per agent.
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
	// Show the telemetry notice only the first time this local config reports.
	// Repeated `init <agent>` runs still send install events, but stay quiet.
	if result.MachineFirst {
		fmt.Printf("%s anonymous aggregate telemetry enabled (disable: ctx-wire telemetry disable)\n", theme.OK.Render("Telemetry"))
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
