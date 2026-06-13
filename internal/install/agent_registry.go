package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// WiringKind classifies how doctor detects an agent's ctx-wire wiring, which
// selects the diagnostic archetype (a hook in a JSON config, a steering rules
// file, or a host plugin file).
type WiringKind int

const (
	// WiringHook is a command hook entry in a settings/config JSON file.
	WiringHook WiringKind = iota
	// WiringRule is a steering rules file containing the `ctx-wire run` guidance.
	WiringRule
	// WiringPlugin is a host plugin file; its presence does not prove the host
	// loaded it, so doctor reports it with that caveat.
	WiringPlugin
	// WiringMCP is an MCP server entry. doctor reports these in its MCP section,
	// not its hooks section.
	WiringMCP
)

// AgentProbe is doctor's read-only view of one agent's wiring detection: the
// marker the install writes (Needle) and the file(s) to look for it in (Paths),
// classified by Kind. It is sourced from the same agentRegistry that drives
// install and uninstall (see AgentProbes), so a new agent is described once.
type AgentProbe struct {
	Name string
	// Label is doctor's display name for this check. Usually Name; MCP agents
	// override it to note their config scope ("vscode (workspace)").
	Label  string
	Kind   WiringKind
	Needle string
	// Paths returns the files to probe for Needle. Empty means the path is
	// unavailable on this OS/setup (doctor renders nothing). More than one entry
	// only for multi-config agents (claude).
	Paths func(workdir string) []string
}

// agentDescriptor describes one AI coding agent ctx-wire knows how to wire,
// unwire, and diagnose. Install/Uninstall are the wiring behaviors; the Probe*
// fields are the read-only detection metadata doctor renders (see AgentProbes).
//
// Install is nil for the three agents (claude, codex, gemini) whose init flow is
// bespoke (multi-step, agent-specific UI) and stays in the command layer; their
// uninstall and detection are still table-driven here. Every other agent's
// install is a uniform "resolve path -> install -> report" closure, so the
// command layer needs no per-agent switch.
type agentDescriptor struct {
	Name string
	// Install wires the agent and returns the configured path (for the user
	// message), whether it changed, and any error. nil = bespoke command-layer
	// flow (claude/codex/gemini). Workdir scopes project-local agents; agents
	// that resolve home-based paths ignore it.
	Install func(workdir string) (path string, changed bool, err error)
	// NeedsWorkdir marks an Install that reads the project directory (a rules or
	// .github/.vscode file under the cwd). Only for these is the working
	// directory resolved, so a home/global agent (cursor, opencode, ...) still
	// wires when the cwd is unavailable, matching the pre-registry behavior.
	NeedsWorkdir bool
	Uninstall    func(workdir string, r *IntegrationUninstallReport) error
	// Probe* describe how doctor detects this agent's ctx-wire wiring: the
	// marker the install writes (ProbeNeedle) and the file(s) to look for it in
	// (ProbePaths), classified by ProbeKind. ProbePaths left nil for agents
	// doctor does not diagnose in its hooks section (vscode/visualstudio are MCP,
	// handled separately). AgentProbes exposes the populated ones to doctor.
	ProbeKind   WiringKind
	ProbeNeedle string
	ProbePaths  func(workdir string) []string
	// ProbeLabel overrides the doctor display label (default: Name). Used by MCP
	// agents to note their config scope, e.g. "vscode (workspace)".
	ProbeLabel string
}

// agentRegistry is the single source of truth for per-agent install, uninstall,
// and detection. The command layer (init), UninstallIntegrations/UninstallAgent,
// and doctor (via AgentProbes) all consume this table, so each agent's behavior
// lives in exactly one place. Order matches the previous iteration order.
var agentRegistry = []agentDescriptor{
	{
		Name: "claude",
		// Install nil: claude's init wires every detected config dir plus MCP
		// auto-wrap and the capture experiment, a bespoke command-layer flow.
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			dirs, err := ClaudeConfigDirs()
			if err != nil {
				return nil // path unavailable on this OS/setup
			}
			for _, dir := range dirs {
				path := filepath.Join(dir, "settings.json")
				changed, cerr := UninstallClaude(path)
				if cerr != nil {
					return cerr
				}
				if changed {
					r.Removed = append(r.Removed, "claude:"+dir)
				}
				memPath := filepath.Join(dir, "CLAUDE.md")
				if err := r.removeInstr("claude instructions:"+dir, memPath); err != nil {
					return err
				}
			}
			return nil
		},
		ProbeKind:   WiringHook,
		ProbeNeedle: claudeHookCommand,
		ProbePaths: func(workdir string) []string {
			dirs, err := ClaudeConfigDirs()
			if err != nil {
				return nil
			}
			paths := make([]string, 0, len(dirs))
			for _, dir := range dirs {
				paths = append(paths, filepath.Join(dir, "settings.json"))
			}
			return paths
		},
	},

	{
		Name: "cursor",
		Install: func(workdir string) (string, bool, error) {
			path, err := CursorHooksPath()
			if err != nil {
				return "", false, err
			}
			changed, err := InstallCursor(path)
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			path, err := CursorHooksPath()
			if err != nil {
				return nil
			}
			changed, err := UninstallCursor(path)
			if err != nil {
				return err
			}
			if changed {
				r.Removed = append(r.Removed, "cursor")
			}
			return nil
		},
		ProbeKind:   WiringHook,
		ProbeNeedle: cursorHookCommand,
		ProbePaths:  singleProbePath(CursorHooksPath),
	},

	{
		Name: "codex",
		// Install nil: codex's init surfaces the trust/feature steps and the
		// agent-attribution write, a bespoke command-layer flow.
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			if path, err := CodexHooksPath(); err == nil {
				changed, err := UninstallCodexHooks(path)
				if err != nil {
					return err
				}
				if changed {
					r.Removed = append(r.Removed, "codex")
				}
			}
			if path, err := CodexConfigPath(); err == nil {
				res, err := UninstallCodexAgentEnv(path)
				if err != nil {
					return err
				}
				switch res {
				case CodexEnvUpdated:
					r.Removed = append(r.Removed, "codex agent env")
				case CodexEnvManual:
					r.Skipped = append(r.Skipped, path)
				}
			}
			if p, err := CodexAgentsPath(); err == nil {
				if err := r.removeInstr("codex instructions", p); err != nil {
					return err
				}
			}
			return nil
		},
		ProbeKind:   WiringHook,
		ProbeNeedle: codexHookCommand,
		ProbePaths:  singleProbePath(CodexHooksPath),
	},

	{
		Name: "gemini",
		// Install nil: gemini's init installs the hook wrapper script plus
		// settings wiring, a bespoke command-layer flow.
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			hookPath, err := GeminiHookPath()
			if err != nil {
				return nil
			}
			if settingsPath, err := GeminiSettingsPath(); err == nil {
				changed, err := UninstallGeminiSettings(settingsPath, hookPath)
				if err != nil {
					return err
				}
				if changed {
					r.Removed = append(r.Removed, "gemini settings")
				}
			}
			removed, skipped, err := UninstallGeminiHook(hookPath)
			if err != nil {
				return err
			}
			if removed {
				r.Removed = append(r.Removed, "gemini hook")
			}
			if skipped {
				r.Skipped = append(r.Skipped, hookPath)
			}
			if p, err := GeminiMemoryPath(); err == nil {
				if err := r.removeInstr("gemini instructions", p); err != nil {
					return err
				}
			}
			return nil
		},
		ProbeKind:   WiringHook,
		ProbeNeedle: "ctx-wire-hook-gemini.sh",
		ProbePaths:  singleProbePath(GeminiSettingsPath),
	},

	{
		Name:         "cline",
		NeedsWorkdir: true,
		Install: func(workdir string) (string, bool, error) {
			path := ClineRulesPath(workdir)
			changed, err := InstallCline(path)
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			return r.removeInstr("cline rules", ClineRulesPath(workdir))
		},
		ProbeKind:   WiringRule,
		ProbeNeedle: "ctx-wire run",
		ProbePaths:  workdirProbePath(ClineRulesPath),
	},

	{
		Name:         "windsurf",
		NeedsWorkdir: true,
		Install: func(workdir string) (string, bool, error) {
			path := WindsurfRulesPath(workdir)
			changed, err := InstallWindsurf(path)
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			return r.removeInstr("windsurf rules", WindsurfRulesPath(workdir))
		},
		ProbeKind:   WiringRule,
		ProbeNeedle: "ctx-wire run",
		ProbePaths:  workdirProbePath(WindsurfRulesPath),
	},

	{
		Name:         "copilot",
		NeedsWorkdir: true,
		Install: func(workdir string) (string, bool, error) {
			// The display path is the .github dir; the install writes both the
			// instructions file and the hook under it.
			path := filepath.Join(workdir, ".github")
			changed, err := InstallCopilot(CopilotInstructionsPath(workdir), CopilotHookPath(workdir))
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			changed, err := UninstallCopilotHook(CopilotHookPath(workdir))
			if err != nil {
				return err
			}
			if changed {
				r.Removed = append(r.Removed, "copilot hook")
			}
			return r.removeInstr("copilot instructions", CopilotInstructionsPath(workdir))
		},
		ProbeKind:   WiringHook,
		ProbeNeedle: "ctx-wire hook copilot",
		ProbePaths:  workdirProbePath(CopilotHookPath),
	},

	{
		Name:         "kilocode",
		NeedsWorkdir: true,
		Install: func(workdir string) (string, bool, error) {
			path := KilocodeRulesPath(workdir)
			changed, err := InstallKilocode(path)
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			return r.removeInstr("kilocode rules", KilocodeRulesPath(workdir))
		},
		ProbeKind:   WiringRule,
		ProbeNeedle: "ctx-wire run",
		ProbePaths:  workdirProbePath(KilocodeRulesPath),
	},

	{
		Name:         "antigravity",
		NeedsWorkdir: true,
		Install: func(workdir string) (string, bool, error) {
			path := AntigravityRulesPath(workdir)
			changed, err := InstallAntigravity(path)
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			return r.removeInstr("antigravity rules", AntigravityRulesPath(workdir))
		},
		ProbeKind:   WiringRule,
		ProbeNeedle: "ctx-wire run",
		ProbePaths:  workdirProbePath(AntigravityRulesPath),
	},

	{
		Name:         "vscode",
		NeedsWorkdir: true,
		Install: func(workdir string) (string, bool, error) {
			path := VSCodeMCPPath(workdir)
			changed, err := InstallMCP(path, "vscode")
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			changed, err := UninstallMCP(VSCodeMCPPath(workdir))
			if err != nil {
				return err
			}
			if changed {
				r.Removed = append(r.Removed, "vscode mcp")
			}
			return nil
		},
		ProbeKind:   WiringMCP,
		ProbeNeedle: "ctx-wire",
		ProbeLabel:  "vscode (workspace)",
		ProbePaths:  workdirProbePath(VSCodeMCPPath),
	},

	{
		Name: "visualstudio",
		Install: func(workdir string) (string, bool, error) {
			path, err := VisualStudioMCPPath()
			if err != nil {
				return "", false, err
			}
			changed, err := InstallMCP(path, "visualstudio")
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			path, err := VisualStudioMCPPath()
			if err != nil {
				return nil
			}
			changed, err := UninstallMCP(path)
			if err != nil {
				return err
			}
			if changed {
				r.Removed = append(r.Removed, "visualstudio mcp")
			}
			return nil
		},
		ProbeKind:   WiringMCP,
		ProbeNeedle: "ctx-wire",
		ProbeLabel:  "visualstudio (user)",
		ProbePaths:  singleProbePath(VisualStudioMCPPath),
	},

	{
		Name: "opencode",
		Install: func(workdir string) (string, bool, error) {
			path, err := OpenCodePluginPath()
			if err != nil {
				return "", false, err
			}
			changed, err := InstallOpenCode(path)
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			path, err := OpenCodePluginPath()
			if err != nil {
				return nil
			}
			if removeFileIfContent(path, opencodePlugin) {
				r.Removed = append(r.Removed, "opencode plugin")
			}
			return nil
		},
		ProbeKind:   WiringPlugin,
		ProbeNeedle: "ctx-wire",
		ProbePaths:  singleProbePath(OpenCodePluginPath),
	},

	{
		Name: "pi",
		Install: func(workdir string) (string, bool, error) {
			path, err := PiPluginPath()
			if err != nil {
				return "", false, err
			}
			changed, err := InstallPi(path)
			return path, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			path, err := PiPluginPath()
			if err != nil {
				return nil
			}
			if removeFileIfContent(path, piPlugin) {
				r.Removed = append(r.Removed, "pi extension")
			}
			return nil
		},
		ProbeKind:   WiringPlugin,
		ProbeNeedle: "ctx-wire",
		ProbePaths:  singleProbePath(PiPluginPath),
	},

	{
		Name: "hermes",
		Install: func(workdir string) (string, bool, error) {
			dir, err := HermesPluginDir()
			if err != nil {
				return "", false, err
			}
			changed, err := InstallHermes(dir)
			return dir, changed, err
		},
		Uninstall: func(workdir string, r *IntegrationUninstallReport) error {
			dir, err := HermesPluginDir()
			if err != nil {
				return nil
			}
			if removeFileIfContent(filepath.Join(dir, "__init__.py"), hermesPluginInit) {
				_ = os.RemoveAll(dir)
				r.Removed = append(r.Removed, "hermes plugin")
			}
			return nil
		},
		ProbeKind:   WiringPlugin,
		ProbeNeedle: "ctx-wire",
		ProbePaths: func(workdir string) []string {
			dir, err := HermesPluginDir()
			if err != nil {
				return nil
			}
			return []string{filepath.Join(dir, "__init__.py")}
		},
	},
}

// singleProbePath adapts a no-arg path resolver (CursorHooksPath, etc.) into a
// ProbePaths func: one path when the resolver succeeds, none when it errors.
func singleProbePath(resolve func() (string, error)) func(string) []string {
	return func(string) []string {
		p, err := resolve()
		if err != nil {
			return nil
		}
		return []string{p}
	}
}

// workdirProbePath adapts a workdir-relative path resolver (ClineRulesPath, etc.)
// into a ProbePaths func. These resolvers never error (pure filepath.Join), so
// they always yield exactly one path.
func workdirProbePath(resolve func(string) string) func(string) []string {
	return func(workdir string) []string {
		return []string{resolve(workdir)}
	}
}

// InstallAgent wires the named agent and returns the configured path (for the
// user message), whether it changed, and any error. ok is false when the name
// is unknown or has no registry install (the bespoke agents claude/codex/gemini,
// which the command layer wires directly).
//
// workdirFn lazily provides the project directory and is invoked ONLY for
// workdir-scoped agents (NeedsWorkdir). So an unknown agent, a bespoke agent, or
// a home/global agent (cursor, visualstudio, opencode, pi, hermes) never touches
// the cwd: a deleted/unreadable working directory cannot block them, and an
// unknown name reports "unsupported" rather than a cwd error. A workdirFn error
// is returned with ok=true (it is a real failure of a known agent).
func InstallAgent(name string, workdirFn func() (string, error)) (path string, changed bool, ok bool, err error) {
	d, found := registryByName(name)
	if !found || d.Install == nil {
		return "", false, false, nil
	}
	workdir := ""
	if d.NeedsWorkdir {
		if workdir, err = workdirFn(); err != nil {
			return "", false, true, err
		}
	}
	path, changed, err = d.Install(workdir)
	return path, changed, true, err
}

// AgentProbes returns the read-only detection metadata for every agent doctor
// diagnoses in its hooks section, in registry order. Agents with no hooks-section
// probe (vscode/visualstudio, which are MCP) are omitted. Both doctor's
// hooksSection and its shim-coverage check consume this, so the per-agent marker
// and path live in exactly one place.
func AgentProbes() []AgentProbe {
	probes := make([]AgentProbe, 0, len(agentRegistry))
	for _, a := range agentRegistry {
		if a.ProbePaths == nil {
			continue
		}
		label := a.ProbeLabel
		if label == "" {
			label = a.Name
		}
		probes = append(probes, AgentProbe{
			Name:   a.Name,
			Label:  label,
			Kind:   a.ProbeKind,
			Needle: a.ProbeNeedle,
			Paths:  a.ProbePaths,
		})
	}
	return probes
}

// AgentNames returns every agent's canonical name in registry order. The command
// layer uses it for the init help text and the "unsupported agent" message, so
// those lists stay in sync with the table instead of being hand-maintained.
func AgentNames() []string {
	names := make([]string, len(agentRegistry))
	for i, a := range agentRegistry {
		names[i] = a.Name
	}
	return names
}

// registryByName looks up a descriptor by agent name. Returns (desc, true) when
// found, or a zero value and false when the name is not in the table.
func registryByName(name string) (agentDescriptor, bool) {
	for _, a := range agentRegistry {
		if a.Name == name {
			return a, true
		}
	}
	return agentDescriptor{}, false
}

// errUnknownAgent returns the standard error for an unrecognized agent name.
func errUnknownAgent(name string) error {
	return fmt.Errorf("unknown agent %q", name)
}
