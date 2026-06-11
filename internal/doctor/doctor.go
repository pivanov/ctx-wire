// Package doctor implements ctx-wire's read-only self-diagnostic. It inspects
// the installed binary, agent hook/MCP configuration, storage writability,
// project filter trust, and recent capture, and reports per-check status. It
// never mutates configuration or gain/tee data; the only filesystem writes are
// transient writability probes in already-existing directories, removed
// immediately.
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"ctx-wire/internal/config"
	"ctx-wire/internal/filter"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/install"
	"ctx-wire/internal/shim"
	"ctx-wire/internal/tee"
	"ctx-wire/internal/telemetry"
	"ctx-wire/internal/ui"
)

// Status is the outcome of a single check.
type Status int

const (
	// OK means the check passed.
	OK Status = iota
	// Warn means a real non-fatal issue that wants attention (e.g. a hook is
	// installed but its feature flag is off, or PATH ordering is wrong).
	Warn
	// Fail means a broken install: unwritable storage or unloadable registry.
	Fail
	// Off means an optional integration is simply not set up. It is informational,
	// not a problem, so it renders neutrally and never affects health: you only
	// see it so you know the integration exists and how to enable it.
	Off
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	case Off:
		return "off"
	default:
		return "?"
	}
}

// Check is one diagnostic line.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Section groups related checks.
type Section struct {
	Title  string
	Checks []Check
}

// Report is the full diagnostic result.
type Report struct {
	Sections []Section
}

// Options configures a doctor run.
type Options struct {
	// Version, Commit, Date are the build metadata of the running binary.
	Version, Commit, Date string
	// Workdir is the directory used to resolve project-local config (cwd).
	Workdir string
	// Recent is how many recent scrubbed commands to list. 0 means counts-only.
	Recent int
}

// Healthy reports whether the report has no failing checks (warnings are fine).
func (r *Report) Healthy() bool {
	for _, sec := range r.Sections {
		for _, c := range sec.Checks {
			if c.Status == Fail {
				return false
			}
		}
	}
	return true
}

// Run collects all diagnostics. It is read-only aside from transient writability
// probes in existing directories.
func Run(opts Options) *Report {
	r := &Report{}
	r.Sections = append(r.Sections,
		binarySection(opts),
		shimsSection(opts),
		hooksSection(opts),
		mcpSection(opts),
		telemetrySection(),
		storageSection(),
		filtersSection(opts),
		captureSection(opts),
	)
	return r
}

func binarySection(opts Options) Section {
	sec := Section{Title: "binary"}
	sec.Checks = append(sec.Checks, Check{
		Name:   "version",
		Status: OK,
		Detail: fmt.Sprintf("%s (commit %s, built %s)", opts.Version, opts.Commit, opts.Date),
	})

	exe, err := os.Executable()
	if err != nil {
		sec.Checks = append(sec.Checks, Check{"resolved path", Warn, "cannot resolve: " + err.Error()})
		return sec
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	sec.Checks = append(sec.Checks, Check{"resolved path", OK, exe})

	// Does `ctx-wire` on PATH resolve to this same binary?
	switch onPath, perr := exec.LookPath("ctx-wire"); {
	case perr != nil:
		sec.Checks = append(sec.Checks, Check{"PATH", Warn, "`ctx-wire` not found on PATH"})
	default:
		if resolved, rerr := filepath.EvalSymlinks(onPath); rerr == nil {
			onPath = resolved
		}
		if onPath == exe {
			sec.Checks = append(sec.Checks, Check{"PATH", OK, "PATH `ctx-wire` is this binary"})
		} else {
			sec.Checks = append(sec.Checks, Check{"PATH", Warn,
				fmt.Sprintf("PATH `ctx-wire` is %s, not this binary (run `ctx-wire init <agent>`)", onPath)})
		}
	}
	if bins := shim.CtxWireBinariesOnPATH(); len(bins) > 1 {
		sec.Checks = append(sec.Checks, Check{"duplicates", Warn,
			fmt.Sprintf("%d ctx-wire binaries on PATH: %s; remove the stale one (a leftover install can shadow this binary and cause shim recursion)",
				len(bins), strings.Join(bins, ", "))})
	}
	return sec
}

func telemetrySection() Section {
	sec := Section{Title: "telemetry"}
	status, err := telemetry.GetStatus()
	if err != nil {
		sec.Checks = append(sec.Checks, Check{"status", Warn, "cannot read: " + err.Error()})
		return sec
	}
	if status.Enabled {
		detail := "enabled: aggregate counters only"
		if status.ForcedByEnv {
			detail += " (from CTX_WIRE_TELEMETRY)"
		}
		sec.Checks = append(sec.Checks, Check{"status", OK, detail})
	} else {
		detail := "disabled"
		if status.ForcedByEnv {
			detail += " (from CTX_WIRE_TELEMETRY)"
		}
		sec.Checks = append(sec.Checks, Check{"status", OK, detail})
	}
	sec.Checks = append(sec.Checks, Check{"endpoint", OK, display(status.Endpoint)})
	return sec
}

func shimsSection(opts Options) Section {
	sec := Section{Title: "shims"}
	dest, err := install.SelfInstallPath()
	if err != nil {
		sec.Checks = append(sec.Checks, Check{"install dir", Warn, "cannot resolve: " + err.Error()})
		return sec
	}
	dir := filepath.Dir(dest)
	st := shim.Inspect(dir, shim.DefaultCommands)
	total := len(st.Commands)
	installed := len(st.Installed)
	// A hook/plugin-capable agent (claude, codex, cursor, ...) is wired through its
	// hook, not PATH shims, so for it zero shims is the correct state, not a problem.
	// Compute coverage once and reuse it for the install, PATH, and usage checks.
	hookCovered := hookOrPluginConfigured(opts)
	sec.Checks = append(sec.Checks, shimInstalledCheck(installed, total, len(st.Skipped), display(st.Dir), hookCovered))
	if len(st.Missing) > 0 {
		sec.Checks = append(sec.Checks, Check{"missing tools", OK,
			fmt.Sprintf("%d candidate command(s) not installed; no shims created", len(st.Missing))})
	}
	if len(st.Skipped) > 0 {
		sec.Checks = append(sec.Checks, Check{"conflicts", Warn,
			"existing non-ctx-wire files skipped: " + strings.Join(st.Skipped, ", ")})
	}
	if shimDirs := shim.ManagedShimDirsOnPATH(); len(shimDirs) > 1 {
		sec.Checks = append(sec.Checks, Check{"duplicates", Warn,
			fmt.Sprintf("managed shims in %d PATH dirs: %s; an upgrade left a stale set (remove the old dir's shims to avoid recursion)",
				len(shimDirs), strings.Join(shimDirs, ", "))})
	}
	// Aggregate across every managed shim dir on PATH (not just the install dir):
	// a stale earlier dir can be the one actually first on PATH and slowing prompts.
	aggInstalled, aggActive, _, _ := shim.AggregateStatus(shim.ManagedDirsWith(dir))
	sec.Checks = append(sec.Checks, shimPathChecks(dir, aggInstalled, aggActive, total, hookCovered)...)
	if missing := missingSystemPathDirs(); len(missing) > 0 {
		sec.Checks = append(sec.Checks, Check{"system PATH", Warn,
			fmt.Sprintf("PATH is missing %s; a shim then cannot find the real command (you get \"no real X on PATH\"), and tools like git cannot find ssh. This is an environment issue, not ctx-wire; fix your shell PATH",
				strings.Join(missing, " and "))})
	}
	switch {
	case st.Uses > 0:
		sec.Checks = append(sec.Checks, Check{"usage", OK,
			fmt.Sprintf("%d shim capture(s) recorded; last %s", st.Uses, st.LastUse)})
	case hookCovered && installed == 0:
		// No shims by design for a hook-covered agent, so no captures is expected;
		// staying silent avoids a false "nothing is working" alarm.
	default:
		sec.Checks = append(sec.Checks, Check{"usage", Warn, "no shim captures recorded yet"})
	}
	return sec
}

// shimInstalledCheck reports the "installed" check for the shims section. Zero
// shims is only actionable when nothing already covers the agent: a hook/plugin-
// capable agent (claude, codex, cursor, ...) is wired through its hook, so for it
// zero shims is the correct state, not a problem. Pure so the decision is
// unit-testable without the filesystem; dir is already display-formatted.
func shimInstalledCheck(installed, total, skipped int, dir string, hookCovered bool) Check {
	switch {
	case installed == total && skipped == 0:
		return Check{"installed", OK,
			fmt.Sprintf("%d/%d managed command shims in %s", installed, total, dir)}
	case installed > 0:
		return Check{"installed", OK,
			fmt.Sprintf("%d/%d managed command shims in %s; missing commands are not shimmed", installed, total, dir)}
	case hookCovered:
		return Check{"installed", Off,
			fmt.Sprintf("no managed command shims in %s; not needed, a hook/plugin covers this agent", dir)}
	default:
		return Check{"installed", Warn,
			fmt.Sprintf("no managed command shims in %s and no hook/plugin configured; run `ctx-wire init <agent>`", dir)}
	}
}

// shimPathChecks decides the PATH / startup-cost advisory from the resolution
// ground truth (st.Active = managed commands that actually resolve to a shim,
// i.e. shims on the shell's hot path) and whether a hook/plugin already covers
// commands. It is pure so the decision is unit-testable without the filesystem.
// The key case: shims resolve first AND a hook/plugin already covers them, so
// they are redundant work that slows every prompt render (the common "slow
// terminal" report); recommend removing them.
func shimPathChecks(dir string, installed, active, total int, hookCovered bool) []Check {
	switch {
	case active > 0 && hookCovered:
		return []Check{{"startup cost", Warn, fmt.Sprintf(
			"%d managed command(s) resolve to ctx-wire shims, but a hook/plugin already covers them, so each adds ~15ms at every shell prompt. Unless you also rely on a steering-only agent (cline/windsurf/kilocode/antigravity/vscode/visualstudio), remove them with `ctx-wire shims uninstall`",
			active)}}
	case active > 0:
		return []Check{{"PATH", OK, fmt.Sprintf("%d/%d shims are first on PATH", active, total)}}
	case installed > 0 && hookCovered:
		return []Check{{"shims", Off,
			"installed but a hook/plugin already covers commands and the real tools resolve first, so they cost nothing; remove with `ctx-wire shims uninstall` if unused"}}
	case installed > 0:
		return []Check{{"PATH", Warn,
			"shims installed but not first on PATH; put " + display(dir) + " before system tool dirs"}}
	}
	return nil
}

// hookOrPluginConfigured reports whether any hook- or plugin-capable agent
// integration is actually present. doctor uses it to tell that installed PATH
// shims are redundant coverage (hence pure shell-startup overhead) rather than a
// steering-only agent's sole path. Needles match hooksSection's own checks.
func hookOrPluginConfigured(opts Options) bool {
	has := func(p, needle string) bool {
		c, e := fileContains(p, needle)
		return e == nil && c
	}
	if p, err := install.ClaudeSettingsPath(); err == nil && has(p, "ctx-wire hook claude") {
		return true
	}
	if p, err := install.CursorHooksPath(); err == nil && has(p, "ctx-wire hook cursor") {
		return true
	}
	if p, err := install.CodexHooksPath(); err == nil && has(p, "ctx-wire hook codex") {
		return true
	}
	if p, err := install.GeminiSettingsPath(); err == nil && has(p, "ctx-wire-hook-gemini.sh") {
		return true
	}
	if has(install.CopilotHookPath(opts.Workdir), "ctx-wire hook copilot") {
		return true
	}
	if p, err := install.OpenCodePluginPath(); err == nil && has(p, "ctx-wire") {
		return true
	}
	if p, err := install.PiPluginPath(); err == nil && has(p, "ctx-wire") {
		return true
	}
	if dir, err := install.HermesPluginDir(); err == nil && has(filepath.Join(dir, "__init__.py"), "ctx-wire") {
		return true
	}
	return false
}

// missingSystemPathDirs returns the standard system tool dirs absent from PATH.
// A PATH without /usr/bin or /bin is the environment fault behind "no real git
// on PATH": shims cannot resolve the real command, and tools like git cannot
// find their own ssh. Skipped on Windows, where these dirs do not apply.
func missingSystemPathDirs() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	have := map[string]bool{}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d != "" {
			have[filepath.Clean(d)] = true
		}
	}
	var missing []string
	for _, d := range []string{"/usr/bin", "/bin"} {
		if !have[d] {
			missing = append(missing, d)
		}
	}
	return missing
}

func hooksSection(opts Options) Section {
	sec := Section{Title: "hooks"}

	if p, err := install.ClaudeSettingsPath(); err == nil {
		sec.Checks = append(sec.Checks, hookCheck("claude", p, "ctx-wire hook claude"))
		// The file-tools capture experiment (config-present only; runtime proof
		// is Read/Grep counts falling in `ctx-wire session`).
		if cfg, cerr := config.Load(); cerr == nil {
			if cfg.Hooks.CaptureFileTools {
				sec.Checks = append(sec.Checks, Check{"claude file-tools capture", OK,
					"experiment on: exact-mappable Read/Grep calls redirect to filtered shell commands"})
			} else {
				sec.Checks = append(sec.Checks, Check{"claude file-tools capture", Off,
					"off; opt in with `ctx-wire init claude --capture-files`"})
			}
		}
	}
	if p, err := install.CursorHooksPath(); err == nil {
		sec.Checks = append(sec.Checks, hookCheck("cursor", p, "ctx-wire hook cursor"))
	}
	if p, err := install.CodexHooksPath(); err == nil {
		sec.Checks = append(sec.Checks, hookCheck("codex", p, "ctx-wire hook codex"))
		// Permission posture: ctx-wire is a filter, not a gate. By default it
		// auto-approves the commands it wraps so codex runs uninterrupted; safety
		// stays with codex's own approval policy. CTX_WIRE_CODEX_SAFE=1 restores
		// the audited read/build/test gate.
		sec.Checks = append(sec.Checks, Check{"codex permissions", OK,
			"auto-approves wrapped commands (a filter, not a permission boundary); safety is codex's approval policy. Set CTX_WIRE_CODEX_SAFE=1 for an audited read/build/test gate."})
		// Codex requires the hooks feature enabled and per-hook trust; report the
		// feature flag but never alter trust.
		if cp, cerr := install.CodexConfigPath(); cerr == nil {
			if enabled, eerr := install.CodexHooksEnabled(cp); eerr == nil {
				if enabled {
					sec.Checks = append(sec.Checks, Check{"codex hooks feature", OK, "[features] hooks = true"})
				} else {
					sec.Checks = append(sec.Checks, Check{"codex hooks feature", Warn,
						"disabled; set [features] hooks = true and trust the hook via `/hooks`"})
				}
			}
			// Agent attribution proves config-present only: Codex profiles can
			// override the top-level policy, so end-to-end confirmation is a
			// later gain entry with agent=codex. Only reported when config.toml
			// exists (a codex user), to avoid noise for everyone else.
			if install.CodexAgentEnvConfigured(cp) {
				sec.Checks = append(sec.Checks, Check{"codex agent attribution", OK,
					"CTX_WIRE_AGENT=codex in shell_environment_policy.set (config-present; runtime proof is a gain entry with agent=codex)"})
			} else if _, serr := os.Stat(cp); serr == nil {
				sec.Checks = append(sec.Checks, Check{"codex agent attribution", Warn,
					"not set; run `ctx-wire init codex` so gain attributes direct runs when the sandbox blocks ps"})
			}
		}
	}
	if p, err := install.GeminiSettingsPath(); err == nil {
		sec.Checks = append(sec.Checks, hookCheck("gemini", p, "ctx-wire-hook-gemini.sh"))
	}
	sec.Checks = append(sec.Checks, ruleCheck("cline", install.ClineRulesPath(opts.Workdir), "ctx-wire run"))
	sec.Checks = append(sec.Checks, ruleCheck("windsurf", install.WindsurfRulesPath(opts.Workdir), "ctx-wire run"))
	sec.Checks = append(sec.Checks, hookCheck("copilot", install.CopilotHookPath(opts.Workdir), "ctx-wire hook copilot"))
	// Plugin-based hook-capable agents (OpenCode, Pi, Hermes). These are the most
	// exposed by the shim-narrowing: `ctx-wire init` writes the plugin file, but
	// enabling it is a separate step in the agent's own config, so a present file
	// does not prove the host loaded it. Surface install state with that caveat.
	if p, err := install.OpenCodePluginPath(); err == nil {
		sec.Checks = append(sec.Checks, pluginCheck("opencode", p, "ctx-wire"))
	}
	if p, err := install.PiPluginPath(); err == nil {
		sec.Checks = append(sec.Checks, pluginCheck("pi", p, "ctx-wire"))
	}
	if dir, err := install.HermesPluginDir(); err == nil {
		sec.Checks = append(sec.Checks, pluginCheck("hermes", filepath.Join(dir, "__init__.py"), "ctx-wire"))
	}
	return sec
}

// pluginCheck reports a plugin-based agent's install state. Unlike a hook, a
// present plugin file does not prove the host enabled/loaded it, so a found
// plugin is reported with that caveat rather than a flat OK.
func pluginCheck(agent, path, needle string) Check {
	notInstalled := "plugin not installed (run `ctx-wire init " + agent + "`); without it this agent has no coverage, hook-capable agents are no longer auto-shimmed (set CTX_WIRE_SHIMS=1 to force)"
	contains, err := fileContains(path, needle)
	switch {
	case err != nil:
		return Check{agent, Off, notInstalled}
	case contains:
		return Check{agent, OK, "plugin file present in " + display(path) + " (enable it in the agent's config; host load not verified here)"}
	default:
		return Check{agent, Off, notInstalled}
	}
}

func hookCheck(agent, path, needle string) Check {
	// hookCheck is for hook-capable agents (claude/cursor/codex/gemini/copilot),
	// which the shim no longer auto-wires under, so a missing hook here means this
	// agent has NO coverage, not the silent shim fallback it used to get.
	notConfigured := "not configured (run `ctx-wire init " + agent + "`); without it this agent gets no coverage, hook-capable agents are no longer auto-shimmed (set CTX_WIRE_SHIMS=1 to force)"
	contains, err := fileContains(path, needle)
	switch {
	case err != nil:
		return Check{agent, Off, notConfigured}
	case contains:
		return Check{agent, OK, "hook present in " + display(path)}
	default:
		return Check{agent, Off, notConfigured}
	}
}

func ruleCheck(agent, path, needle string) Check {
	contains, err := fileContains(path, needle)
	switch {
	case err != nil:
		return Check{agent, Off, "not configured (run `ctx-wire init " + agent + "` to enable)"}
	case contains:
		return Check{agent, OK, "ctx-wire guidance present in " + display(path)}
	default:
		return Check{agent, Off, "not configured (run `ctx-wire init " + agent + "` to enable)"}
	}
}

func mcpSection(opts Options) Section {
	sec := Section{Title: "mcp"}
	// VS Code: workspace .vscode/mcp.json.
	vscode := install.VSCodeMCPPath(opts.Workdir)
	sec.Checks = append(sec.Checks, mcpCheck("vscode (workspace)", vscode))
	// Visual Studio: ~/.mcp.json.
	if vs, err := install.VisualStudioMCPPath(); err == nil {
		sec.Checks = append(sec.Checks, mcpCheck("visualstudio (user)", vs))
	}
	sec.Checks = append(sec.Checks, claudeMCPWrapChecks()...)
	return sec
}

// claudeMCPWrapChecks inspects ~/.claude.json for servers relayed through
// ctx-wire mcp-wrap. Wraps pointing at THIS binary are healthy; a wrap whose
// ctx-wire path no longer matches (an old install location) is the one state
// auto-wrap deliberately refuses to touch, so doctor is where it must surface:
// that server breaks the moment the stale binary disappears.
func claudeMCPWrapChecks() []Check {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
	}
	return claudeMCPWrapChecksAt(filepath.Join(home, ".claude.json"), exe)
}

// claudeMCPWrapChecksAt is the pure core, parameterized over the config path
// and the current binary so the healthy/stale distinction is testable (a test
// binary is never named ctx-wire, so os.Executable cannot exercise it).
func claudeMCPWrapChecksAt(configPath, exe string) []Check {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil // no Claude config: nothing to report
	}
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		return nil
	}
	var current, stale []string
	visit := func(servers map[string]any) {
		for name, v := range servers {
			sc, ok := v.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := sc["command"].(string)
			args, _ := sc["args"].([]any)
			first := ""
			if len(args) > 0 {
				first, _ = args[0].(string)
			}
			if !strings.HasSuffix(filepath.Base(cmd), "ctx-wire") || first != "mcp-wrap" {
				continue
			}
			if cmd == exe {
				current = append(current, name)
			} else {
				stale = append(stale, name+" ("+cmd+")")
			}
		}
	}
	if servers, ok := cfg["mcpServers"].(map[string]any); ok {
		visit(servers)
	}
	if projects, ok := cfg["projects"].(map[string]any); ok {
		for _, pv := range projects {
			if pm, ok := pv.(map[string]any); ok {
				if servers, ok := pm["mcpServers"].(map[string]any); ok {
					visit(servers)
				}
			}
		}
	}
	var checks []Check
	sort.Strings(current)
	sort.Strings(stale)
	if len(current) > 0 {
		checks = append(checks, Check{"claude mcp-wrap", OK,
			fmt.Sprintf("%d server(s) relayed through this binary: %s", len(current), strings.Join(dedupeSorted(current), ", "))})
	}
	if len(stale) > 0 {
		checks = append(checks, Check{"claude mcp-wrap", Warn,
			"wrapped by a ctx-wire that is not this binary (breaks if that path disappears): " +
				strings.Join(dedupeSorted(stale), ", ") + "; edit the entry's command back to the server itself (everything after `--`), then `ctx-wire init claude` re-wraps it here"})
	}
	return checks
}

func dedupeSorted(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func mcpCheck(label, path string) Check {
	contains, err := fileContains(path, "ctx-wire")
	switch {
	case err != nil:
		return Check{label, Off, "not configured (" + display(path) + ")"}
	case contains:
		return Check{label, OK, "ctx-wire server in " + display(path)}
	default:
		return Check{label, Off, "not configured (" + display(path) + ")"}
	}
}

func storageSection() Section {
	sec := Section{Title: "storage"}

	if dirs, err := gain.WriteDirs(); err == nil {
		sec.Checks = append(sec.Checks, storageChecks("gain log", dirs)...)
	} else {
		sec.Checks = append(sec.Checks, Check{"gain log", Fail, "cannot resolve path: " + err.Error()})
	}
	if dirs, err := tee.WriteDirs(); err == nil {
		sec.Checks = append(sec.Checks, storageChecks("tee dir", dirs)...)
	} else {
		sec.Checks = append(sec.Checks, Check{"tee dir", Fail, "cannot resolve path: " + err.Error()})
	}
	return sec
}

// storageChecks evaluates an ordered list of candidate write directories (the
// same primary->fallback order used at runtime). A writable primary is OK; a
// writable fallback when the primary is not is a warning (capture still works);
// no writable target is a fatal Fail.
func storageChecks(name string, dirs []string) []Check {
	if len(dirs) == 0 {
		return []Check{{name, Fail, "no write target resolved"}}
	}
	if dirWritable(dirs[0]) {
		return []Check{{name, OK, "writable: " + display(dirs[0])}}
	}
	checks := []Check{{name, Warn, "primary not writable: " + display(dirs[0])}}
	for _, fb := range dirs[1:] {
		if dirWritable(fb) {
			checks = append(checks, Check{name + " fallback", OK, "writable: " + display(fb)})
			return checks
		}
	}
	checks = append(checks, Check{name + " fallback", Fail, "no writable storage target"})
	return checks
}

func filtersSection(opts Options) Section {
	sec := Section{Title: "filters"}

	// Built-in registry must load; this is a fatal install check.
	if _, err := filter.LoadBuiltin(); err != nil {
		sec.Checks = append(sec.Checks, Check{"built-in registry", Fail, "cannot load: " + err.Error()})
	} else {
		sec.Checks = append(sec.Checks, Check{"built-in registry", OK, "loaded"})
	}

	ppath := filter.ProjectFiltersPath(opts.Workdir)
	switch state := filter.TrustState(ppath); state {
	case filter.TrustAbsent:
		sec.Checks = append(sec.Checks, Check{"project filters", OK, "none (" + display(ppath) + ")"})
	case filter.TrustTrusted:
		sec.Checks = append(sec.Checks, Check{"project filters", OK, "trusted: " + display(ppath)})
	case filter.TrustChanged:
		sec.Checks = append(sec.Checks, Check{"project filters", Warn,
			"changed since trusted; re-run `ctx-wire trust` (" + display(ppath) + ")"})
	default: // untrusted
		sec.Checks = append(sec.Checks, Check{"project filters", Warn,
			"present but untrusted; run `ctx-wire trust` (" + display(ppath) + ")"})
	}
	return sec
}

func captureSection(opts Options) Section {
	sec := Section{Title: "recent capture"}

	summary, err := gain.Summarize()
	if err != nil {
		sec.Checks = append(sec.Checks, Check{"gain", Warn, "cannot read: " + err.Error()})
		return sec
	}
	if summary.Commands == 0 {
		sec.Checks = append(sec.Checks, Check{"gain", Warn, "no commands recorded yet"})
		return sec
	}
	sec.Checks = append(sec.Checks, Check{"gain", OK,
		fmt.Sprintf("%d commands recorded, %s saved", summary.Commands, ui.HumanBytes(summary.SavedBytes))})

	if opts.Recent > 0 {
		entries, rerr := gain.RecentEntries(opts.Recent)
		if rerr != nil {
			sec.Checks = append(sec.Checks, Check{"recent", Warn, "cannot read: " + rerr.Error()})
			return sec
		}
		for _, e := range entries {
			sec.Checks = append(sec.Checks, Check{"recent", OK,
				fmt.Sprintf("%s  %s", e.TS, e.Command)})
		}
	}
	return sec
}

// Format renders a report as concise plain-text status lines.
func Format(r *Report) string {
	return FormatThemed(r, ui.Plain(), true)
}

// FormatThemed renders a report as concise terminal status lines. By default
// Off rows (optional integrations that simply are not set up) are hidden behind
// a one-line count so the screen shows only actionable state; showAll restores
// them (`doctor --all`). Hiding never affects health: Off is informational.
func FormatThemed(r *Report, theme ui.Theme, showAll bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.Heading("ctx-wire doctor: health check"))
	hidden := 0
	for _, sec := range r.Sections {
		checks := sec.Checks
		if !showAll {
			visible := make([]Check, 0, len(checks))
			for _, c := range checks {
				if c.Status == Off {
					hidden++
					continue
				}
				visible = append(visible, c)
			}
			checks = visible
			if len(checks) == 0 {
				continue // a section of nothing-but-off says nothing actionable
			}
		}
		title := sec.Title
		if title == "mcp" {
			title = "MCP"
		} else if title != "" {
			title = strings.ToUpper(title[:1]) + title[1:]
		}
		fmt.Fprintf(&b, "\n%s\n", theme.Section.Render(title))
		for _, c := range checks {
			fmt.Fprintf(&b, "  %s %-22s %s\n",
				theme.Status(c.Status.String()),
				theme.Label.Render(c.Name),
				colorDetail(theme, c.Detail))
		}
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "\n%s\n", theme.Dim.Render(fmt.Sprintf(
			"%d optional check(s) hidden (not configured / not needed) · ctx-wire doctor --all", hidden)))
	}
	if r.Healthy() {
		fmt.Fprintf(&b, "\n%s\n", theme.OK.Render("healthy"))
	} else {
		fmt.Fprintf(&b, "\n%s\n", theme.Fail.Render("issues found (see [fail] lines)"))
	}
	return b.String()
}

func colorDetail(theme ui.Theme, detail string) string {
	if !theme.Color {
		return detail
	}
	switch {
	case strings.HasPrefix(detail, "writable: "):
		return "writable: " + theme.Path.Render(strings.TrimPrefix(detail, "writable: "))
	case strings.HasPrefix(detail, "primary not writable: "):
		return "primary not writable: " + theme.Path.Render(strings.TrimPrefix(detail, "primary not writable: "))
	case strings.Contains(detail, "commands recorded"):
		return theme.Number.Render(detail)
	case strings.Contains(detail, "hook present"):
		return theme.Good.Render(detail)
	case strings.Contains(detail, "not configured"), strings.Contains(detail, "no mcp.json"):
		return theme.Dim.Render(detail)
	default:
		return detail
	}
}

// --- helpers ---

// fileContains reports whether the file at path contains needle. A missing file
// returns os.ErrNotExist so callers can distinguish "no config" from "no hook".
func fileContains(path, needle string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), needle), nil
}

// dirWritable reports whether files can be created under dir. It probes the
// nearest existing ancestor (so it never creates ctx-wire's persistent dirs)
// and removes the probe immediately. If the nearest existing ancestor is a file
// rather than a directory, dir can never be created, so it is not writable.
func dirWritable(dir string) bool {
	anc, isDir := nearestExisting(dir)
	if anc == "" || !isDir {
		return false
	}
	f, err := os.CreateTemp(anc, ".ctx-wire-doctor-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// nearestExisting returns the closest ancestor of dir that exists (dir itself if
// it exists), and whether that ancestor is a directory. Returns ("", false) if
// nothing in the chain exists.
func nearestExisting(dir string) (path string, isDir bool) {
	for {
		if info, err := os.Stat(dir); err == nil {
			return dir, info.IsDir()
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func display(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, rerr := filepath.Rel(home, path); rerr == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}
