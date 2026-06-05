// Package doctor implements ctx-wire's read-only self-diagnostic. It inspects
// the installed binary, agent hook/MCP configuration, storage writability,
// project filter trust, and recent capture, and reports per-check status. It
// never mutates configuration or gain/tee data; the only filesystem writes are
// transient writability probes in already-existing directories, removed
// immediately.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
		shimsSection(),
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

func shimsSection() Section {
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
	switch {
	case installed == total && len(st.Skipped) == 0:
		sec.Checks = append(sec.Checks, Check{"installed", OK,
			fmt.Sprintf("%d/%d managed command shims in %s", installed, total, display(st.Dir))})
	case installed > 0:
		sec.Checks = append(sec.Checks, Check{"installed", OK,
			fmt.Sprintf("%d/%d managed command shims in %s; missing commands are not shimmed", installed, total, display(st.Dir))})
	default:
		sec.Checks = append(sec.Checks, Check{"installed", Warn,
			fmt.Sprintf("no managed command shims in %s; run `ctx-wire init <agent>`", display(st.Dir))})
	}
	if len(st.Missing) > 0 {
		sec.Checks = append(sec.Checks, Check{"missing tools", OK,
			fmt.Sprintf("%d candidate command(s) not installed; no shims created", len(st.Missing))})
	}
	if len(st.Skipped) > 0 {
		sec.Checks = append(sec.Checks, Check{"conflicts", Warn,
			"existing non-ctx-wire files skipped: " + strings.Join(st.Skipped, ", ")})
	}
	if len(st.Active) > 0 {
		sec.Checks = append(sec.Checks, Check{"PATH", OK,
			fmt.Sprintf("%d/%d shims are first on PATH", len(st.Active), total)})
	} else if installed > 0 {
		sec.Checks = append(sec.Checks, Check{"PATH", Warn,
			"shims installed but not first on PATH; put " + display(st.Dir) + " before system tool dirs"})
	}
	if st.Uses > 0 {
		sec.Checks = append(sec.Checks, Check{"usage", OK,
			fmt.Sprintf("%d shim capture(s) recorded; last %s", st.Uses, st.LastUse)})
	} else {
		sec.Checks = append(sec.Checks, Check{"usage", Warn, "no shim captures recorded yet"})
	}
	return sec
}

func hooksSection(opts Options) Section {
	sec := Section{Title: "hooks"}

	if p, err := install.ClaudeSettingsPath(); err == nil {
		sec.Checks = append(sec.Checks, hookCheck("claude", p, "ctx-wire hook claude"))
	}
	if p, err := install.CursorHooksPath(); err == nil {
		sec.Checks = append(sec.Checks, hookCheck("cursor", p, "ctx-wire hook cursor"))
	}
	if p, err := install.CodexHooksPath(); err == nil {
		sec.Checks = append(sec.Checks, hookCheck("codex", p, "ctx-wire hook codex"))
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
		}
	}
	if p, err := install.GeminiSettingsPath(); err == nil {
		sec.Checks = append(sec.Checks, hookCheck("gemini", p, "ctx-wire-hook-gemini.sh"))
	}
	sec.Checks = append(sec.Checks, ruleCheck("cline", install.ClineRulesPath(opts.Workdir), "ctx-wire run"))
	sec.Checks = append(sec.Checks, ruleCheck("windsurf", install.WindsurfRulesPath(opts.Workdir), "ctx-wire run"))
	sec.Checks = append(sec.Checks, hookCheck("copilot", install.CopilotHookPath(opts.Workdir), "ctx-wire hook copilot"))
	return sec
}

func hookCheck(agent, path, needle string) Check {
	contains, err := fileContains(path, needle)
	switch {
	case err != nil:
		return Check{agent, Off, "not configured (run `ctx-wire init " + agent + "` to enable)"}
	case contains:
		return Check{agent, OK, "hook present in " + display(path)}
	default:
		return Check{agent, Off, "not configured (run `ctx-wire init " + agent + "` to enable)"}
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
	return sec
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
	return FormatThemed(r, ui.Plain())
}

// FormatThemed renders a report as concise terminal status lines.
func FormatThemed(r *Report, theme ui.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.Heading("ctx-wire doctor"))
	for _, sec := range r.Sections {
		fmt.Fprintf(&b, "\n%s\n", theme.Section.Render(sec.Title))
		for _, c := range sec.Checks {
			fmt.Fprintf(&b, "  %s %-22s %s\n",
				theme.Status(c.Status.String()),
				theme.Label.Render(c.Name),
				colorDetail(theme, c.Detail))
		}
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
