package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ctx-wire/internal/install"
	"ctx-wire/internal/shim"
)

// maybeAdviseRedundantShims prints a one-time nudge (it NEVER deletes) when the
// installed PATH shims are redundant for a hook/plugin user and actually on the
// shell's hot path, so existing users learn they can speed up their prompt with
// one command after upgrading. Migration is advisory, not automatic: removing
// global shims could break a steering agent configured in another project that
// has not run yet, which no local signal can rule out, so ctx-wire surfaces the
// fix and lets the user decide.
//
// It nudges only when ALL hold:
//   - a real installed release build (never a dev build);
//   - the shims were NOT installed deliberately (no keep-marker from a steering
//     agent's init or an explicit `shims install`);
//   - a hook/plugin integration already covers model-visible commands;
//   - the shims have NO recorded use (aggregate Uses == 0: only the shim template
//     sets CTX_WIRE_SHIM, recorded for every invocation incl. bypassed commands,
//     so a hook-only user reads 0 while any steering use reads > 0);
//   - a managed command actually resolves to a shim (aggregate Active > 0), so
//     they are really on the hot path, not merely installed-but-shadowed;
//   - no steering-only agent is configured in this project.
//
// It nudges at most once (a marker prevents nagging on every `gain`), and self
// resolves: once the user runs `ctx-wire shims uninstall` the shims leave the hot
// path. Opt out with CTX_WIRE_KEEP_SHIMS=1.
func maybeAdviseRedundantShims(subcommand string) {
	switch subcommand {
	case "gain", "update": // doctor surfaces the same advisory in its report
	default:
		return
	}
	if v := os.Getenv("CTX_WIRE_KEEP_SHIMS"); v != "" && v != "0" && v != "false" {
		return
	}
	if _, ok := stableCurrentBinaryPath(); !ok {
		return // dev build or not the installed binary
	}
	dest, err := install.SelfInstallPath()
	if err != nil {
		return
	}
	dirs := shim.ManagedDirsWith(filepath.Dir(dest))
	if shim.WantsKeep(dirs) {
		return // shims installed deliberately: never nudge to remove them
	}
	installed, active, uses, _ := shim.AggregateStatus(dirs)
	// Cheap gate first: skip the agent-config reads unless there are unused shims
	// actually on the hot path. Uses > 0 (a steering agent relied on them) is the
	// common case on an active machine, so this returns fast.
	if installed == 0 || active == 0 || uses > 0 {
		return
	}
	wd, _ := os.Getwd()
	if !shouldFlagRedundantShims(installed, active, uses, hookOrPluginCoverageConfigured(wd), steeringConfiguredHere(wd)) {
		return
	}
	// Nudge at most once so frequent `gain` runs do not nag.
	nudged := filepath.Join(filepath.Dir(dest), nudgeMarkerName)
	if _, err := os.Stat(nudged); err == nil {
		return
	}
	theme := themeForFile(os.Stderr)
	fmt.Fprintf(os.Stderr,
		"%s %d PATH shim(s) resolve first and slow your shell prompt, but a hook/plugin already covers these commands. Remove them with `ctx-wire shims uninstall` (keep them: `ctx-wire shims install`).\n",
		theme.Dim.Render("ctx-wire:"), active)
	_ = os.WriteFile(nudged, []byte("advised once about redundant shims\n"), 0o644)
}

// nudgeMarkerName records that the redundant-shims advisory already fired once, so
// it does not nag on every `gain`. It is reset whenever the shim set changes
// (install or uninstall), so a later experimental reinstall can be advised again.
const nudgeMarkerName = ".ctx-wire-shims-nudged"

// clearNudgeMarker resets the once-only advisory state. Best-effort.
func clearNudgeMarker(installDir string) {
	_ = os.Remove(filepath.Join(installDir, nudgeMarkerName))
}

// shouldFlagRedundantShims is the pure decision behind the redundant-shims advisory
// (see maybeAdviseRedundantShims for the rationale of each gate). Kept separate so
// the policy is unit-testable without the filesystem. Counts are aggregated across
// every managed shim dir.
func shouldFlagRedundantShims(installed, active, uses int, hookCovered, steeringHere bool) bool {
	return installed > 0 && // there are managed shims
		active > 0 && // at least one resolves to a shim (real hot-path cost)
		uses == 0 && // no recorded shim use (no steering agent relied on them)
		hookCovered && // a hook/plugin already covers commands
		!steeringHere // and no steering-only agent needs them here
}

func fileHasNeedle(path, needle string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), needle)
}

// hookOrPluginCoverageConfigured reports whether any hook/plugin-capable agent is
// actually wired, the signal that shims are redundant coverage. Needles match the
// install writers and the doctor checks.
func hookOrPluginCoverageConfigured(wd string) bool {
	if p, err := install.ClaudeSettingsPath(); err == nil && fileHasNeedle(p, "ctx-wire hook claude") {
		return true
	}
	if p, err := install.CursorHooksPath(); err == nil && fileHasNeedle(p, "ctx-wire hook cursor") {
		return true
	}
	if p, err := install.CodexHooksPath(); err == nil && fileHasNeedle(p, "ctx-wire hook codex") {
		return true
	}
	if p, err := install.GeminiSettingsPath(); err == nil && fileHasNeedle(p, "ctx-wire-hook-gemini.sh") {
		return true
	}
	if fileHasNeedle(install.CopilotHookPath(wd), "ctx-wire hook copilot") {
		return true
	}
	if p, err := install.OpenCodePluginPath(); err == nil && fileHasNeedle(p, "ctx-wire") {
		return true
	}
	if p, err := install.PiPluginPath(); err == nil && fileHasNeedle(p, "ctx-wire") {
		return true
	}
	if d, err := install.HermesPluginDir(); err == nil && fileHasNeedle(filepath.Join(d, "__init__.py"), "ctx-wire") {
		return true
	}
	return false
}

// steeringConfiguredHere reports whether a steering-only agent (whose shim is its
// only coverage) is configured in this project. If so, the shims are NOT redundant
// and must be kept. Project-local, so it is one more guard on top of the Uses == 0
// ground truth, not the sole signal.
func steeringConfiguredHere(wd string) bool {
	if fileHasNeedle(install.ClineRulesPath(wd), "ctx-wire run") {
		return true
	}
	if fileHasNeedle(install.WindsurfRulesPath(wd), "ctx-wire run") {
		return true
	}
	if fileHasNeedle(install.KilocodeRulesPath(wd), "ctx-wire run") {
		return true
	}
	if fileHasNeedle(install.AntigravityRulesPath(wd), "ctx-wire run") {
		return true
	}
	if fileHasNeedle(install.VSCodeMCPPath(wd), "ctx-wire") {
		return true
	}
	if p, err := install.VisualStudioMCPPath(); err == nil && fileHasNeedle(p, "ctx-wire") {
		return true
	}
	return false
}
