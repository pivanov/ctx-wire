package install

import (
	"errors"
	"path/filepath"
	"testing"
)

// constWorkdir returns a workdirFn that always yields wd. Used where a test wants
// to inject a hermetic project directory instead of the process cwd.
func constWorkdir(wd string) func() (string, error) {
	return func() (string, error) { return wd, nil }
}

// TestInstallAgentRoundTrip verifies that for every agent with a registry
// Install closure, InstallAgent wires it into a hermetic environment and the
// matching Uninstall closure then removes exactly that wiring. This ties the new
// Install closures to the existing Uninstall closures: a path mismatch between
// the two (the precise drift the registry exists to prevent) surfaces here as an
// empty Removed set. The bespoke agents (claude/codex/gemini, Install nil) are
// covered by their command-layer flows and the uninstall consistency test.
func TestInstallAgentRoundTrip(t *testing.T) {
	for _, a := range agentRegistry {
		if a.Install == nil {
			continue // bespoke command-layer flow
		}
		a := a // capture
		t.Run(a.Name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			t.Setenv("GEMINI_HOME", filepath.Join(home, ".gemini"))
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
			t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			workdir := t.TempDir()

			// First install must report a change and a non-empty path.
			path, changed, ok, err := InstallAgent(a.Name, constWorkdir(workdir))
			if err != nil {
				t.Fatalf("InstallAgent(%s): %v", a.Name, err)
			}
			if !ok {
				t.Fatalf("InstallAgent(%s): ok=false, want a registry install", a.Name)
			}
			if !changed {
				t.Fatalf("InstallAgent(%s): changed=false on first install", a.Name)
			}
			if path == "" {
				t.Errorf("InstallAgent(%s): empty configured path", a.Name)
			}

			// Re-running init is a supported no-op: a second install reports no
			// change, which is what drives the "already configured" message.
			if _, changed2, _, err2 := InstallAgent(a.Name, constWorkdir(workdir)); err2 != nil || changed2 {
				t.Errorf("InstallAgent(%s) second run: changed=%v err=%v, want false/nil", a.Name, changed2, err2)
			}

			// The matching Uninstall closure must find and remove what Install
			// wrote; an empty Removed set means the two closures disagree on paths.
			report, err := UninstallAgent(workdir, a.Name)
			if err != nil {
				t.Fatalf("UninstallAgent(%s): %v", a.Name, err)
			}
			if len(report.Removed) == 0 {
				t.Errorf("UninstallAgent(%s): Removed empty after install; install/uninstall paths disagree", a.Name)
			}
		})
	}
}

// TestInstallAgentUnknown verifies an unrecognized name reports ok=false rather
// than a silent no-op the command layer would misreport as success, and that it
// never resolves the working directory (so the command layer reports
// "unsupported", not a cwd error).
func TestInstallAgentUnknown(t *testing.T) {
	_, _, ok, err := InstallAgent("nonesuch", func() (string, error) {
		t.Fatal("InstallAgent(unknown) resolved the working directory; it must not")
		return "", nil
	})
	if ok || err != nil {
		t.Fatalf("InstallAgent(unknown) = ok %v, err %v; want false, nil", ok, err)
	}
}

// TestInstallAgentBespokeHaveNoRegistryInstall pins that claude, codex, and
// gemini stay command-layer-only (Install nil): InstallAgent reports ok=false so
// the command layer keeps dispatching their bespoke flows. If a future change
// adds a registry Install for one of them, this test forces a deliberate review.
func TestInstallAgentBespokeHaveNoRegistryInstall(t *testing.T) {
	for _, name := range []string{"claude", "codex", "gemini"} {
		if _, _, ok, _ := InstallAgent(name, constWorkdir(t.TempDir())); ok {
			t.Errorf("InstallAgent(%q): ok=true, but this agent must stay a bespoke command-layer flow", name)
		}
	}
}

// TestInstallAgentHomeScopedIgnoreWorkdir guards the cwd regression: agents that
// resolve home/global paths (cursor, visualstudio, opencode, pi, hermes) must
// wire without ever consulting the working directory, so a deleted/unreadable
// cwd cannot block them. workdirFn fails the test if it is ever called.
func TestInstallAgentHomeScopedIgnoreWorkdir(t *testing.T) {
	for _, name := range []string{"cursor", "visualstudio", "opencode", "pi", "hermes"} {
		name := name
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
			t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
			_, _, ok, err := InstallAgent(name, func() (string, error) {
				t.Fatalf("InstallAgent(%q) resolved the working directory; home-scoped agents must not", name)
				return "", nil
			})
			if !ok || err != nil {
				t.Fatalf("InstallAgent(%q) = ok %v, err %v; want true, nil", name, ok, err)
			}
		})
	}
}

// TestInstallAgentWorkdirScopedPropagateError guards that workdir-scoped agents
// still depend on the cwd: when workdirFn errors, InstallAgent surfaces it with
// ok=true, so the command layer reports the cwd error (not "unsupported").
func TestInstallAgentWorkdirScopedPropagateError(t *testing.T) {
	wantErr := errors.New("getwd boom")
	_, _, ok, err := InstallAgent("cline", func() (string, error) { return "", wantErr })
	if !ok {
		t.Fatal("InstallAgent(cline): ok=false; a known agent must report ok=true")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("InstallAgent(cline): err = %v; want %v", err, wantErr)
	}
}

// TestEveryNonBespokeAgentHasInstall guards that every agent except the three
// bespoke ones (claude/codex/gemini, wired directly by the command layer) has a
// registry Install closure. Without it, a newly added simple agent that forgot
// its Install would silently fall through to "unsupported agent" at init time.
func TestEveryNonBespokeAgentHasInstall(t *testing.T) {
	bespoke := map[string]bool{"claude": true, "codex": true, "gemini": true}
	for _, a := range agentRegistry {
		if !bespoke[a.Name] && a.Install == nil {
			t.Errorf("agent %q has no registry Install and is not a known bespoke flow; init would report it unsupported", a.Name)
		}
	}
}

// TestAgentNamesCoversRegistry pins that the help/error agent list (AgentNames,
// used by `init --help` and the "unsupported agent" message) stays complete: it
// must list every registry agent, in registry order, so adding an agent updates
// the user-facing lists automatically.
func TestAgentNamesCoversRegistry(t *testing.T) {
	names := AgentNames()
	if len(names) != len(agentRegistry) {
		t.Fatalf("AgentNames len = %d, want %d (registry size)", len(names), len(agentRegistry))
	}
	for i, a := range agentRegistry {
		if names[i] != a.Name {
			t.Errorf("AgentNames[%d] = %q, want %q", i, names[i], a.Name)
		}
	}
}

// TestEveryAgentIsDiagnosed guards that every agent is visible to `ctx-wire
// doctor`: it carries a probe (ProbePaths), rendered in the hooks section
// (Hook/Rule/Plugin) or the MCP section (WiringMCP). A new agent without one
// would be installable but invisible to doctor.
func TestEveryAgentIsDiagnosed(t *testing.T) {
	for _, a := range agentRegistry {
		if a.ProbePaths == nil {
			t.Errorf("agent %q has no doctor probe; doctor would never report it", a.Name)
		}
	}
}
