package install

import (
	"runtime"
	"strings"
	"testing"
)

// The live builder must name the right agent and stay greppable by doctor.
//
// It deliberately does NOT assert isHookCommand here. On Windows hookCommand
// embeds os.Executable(), which under `go test` is the test binary
// (…\b001\test.test.exe), not ctx-wire.exe, so the base-name check in
// isHookCommand rejects it for reasons that have nothing to do with the code
// under test. The round-trip guarantee is covered against realistic executable
// paths by TestHookCommandForCoversEveryPlatformBranch.
func TestHookCommandNamesTheAgent(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "cursor", "copilot"} {
		cmd := hookCommand(agent)
		if !strings.HasSuffix(cmd, " hook "+agent) {
			t.Errorf("hookCommand(%q) = %q, which does not invoke that agent's hook", agent, cmd)
		}
		if !strings.Contains(cmd, HookNeedle(agent)) {
			t.Errorf("hookCommand(%q) = %q, missing probe needle %q (doctor would report it unwired)",
				agent, cmd, HookNeedle(agent))
		}
	}
}

// The absolute form is what fixes the Windows PATH failure; the bare form is
// what every existing install already has on disk. Both must be recognized, or
// upgrading a Windows user orphans their old entry and duplicates the hook.
func TestIsHookCommandAcceptsBothForms(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"bare unix form", "ctx-wire hook claude", true},
		{"absolute windows form", `C:\Users\x\AppData\Local\ctx-wire\bin\ctx-wire.exe hook claude`, true},
		{"quoted windows path with spaces", `"C:\Users\User Name\AppData\Local\ctx-wire\bin\ctx-wire.exe" hook claude`, true},
		{"absolute unix path", "/home/x/.local/bin/ctx-wire hook claude", true},
		{"leading and trailing space", "  ctx-wire hook claude  ", true},

		{"different agent", "ctx-wire hook codex", false},
		{"someone else's hook", "other-tool hook claude", false},
		{"our name inside another binary", "ctx-wire-shim hook claude", false},
		{"not a hook at all", "ctx-wire run git status", false},
		{"empty", "", false},
		{"suffix only", "hook claude", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isHookCommand(c.cmd, "claude"); got != c.want {
				t.Errorf("isHookCommand(%q, claude) = %v, want %v", c.cmd, got, c.want)
			}
		})
	}
}

// On Windows the command must not depend on PATH, which is the whole point of
// the change. On Unix it stays bare so nothing about existing installs moves.
func TestHookCommandIsAbsoluteOnWindowsOnly(t *testing.T) {
	cmd := hookCommand("copilot")
	bare := strings.HasPrefix(cmd, hookExeName+" ")
	if runtime.GOOS == "windows" {
		if bare {
			t.Errorf("hookCommand on Windows = %q; a bare name depends on PATH, which is what breaks", cmd)
		}
		return
	}
	if !bare {
		t.Errorf("hookCommand on %s = %q, want the bare PATH-resolved form", runtime.GOOS, cmd)
	}
}

// A path containing spaces must be quoted, or the agent splits the command on
// whitespace and tries to execute a truncated path. Windows install dirs get
// spaces routinely, via the user's display name in %LOCALAPPDATA%.
func TestHookCommandQuotesSpacedPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("quoting only applies to the Windows absolute form")
	}
	cmd := hookCommand("claude")
	exe := strings.TrimSuffix(cmd, " hook claude")
	if strings.ContainsAny(exe, " \t") && !strings.HasPrefix(exe, `"`) {
		t.Errorf("hookCommand = %q: path contains spaces but is not quoted", cmd)
	}
}

// A spaced Windows path for a PowerShell agent is written with the call
// operator. Detection and uninstall must still recognize that form, or a
// Copilot user with a space in their profile name gets an entry that init
// re-adds and uninstall cannot remove.
func TestIsHookCommandAcceptsCallOperatorForm(t *testing.T) {
	cmd := `& "C:\Users\User Name\AppData\Local\ctx-wire\bin\ctx-wire.exe" hook copilot`
	if !isHookCommand(cmd, "copilot") {
		t.Errorf("isHookCommand did not recognize the PowerShell call-operator form: %s", cmd)
	}
	if isHookCommand(cmd, "claude") {
		t.Error("call-operator copilot entry must not match a different agent")
	}
}

// Every agent whose hook command comes from hookCommand must have a ProbeNeedle
// that actually matches it. Copilot's was left as the literal
// "ctx-wire hook copilot" when the others moved to HookNeedle, so on Windows
// (where the command is an absolute path) doctor reported a correctly-wired
// install as unwired. Derive the check from the registry so a new hook agent
// cannot reintroduce the drift.
func TestHookProbeNeedlesMatchTheInstalledCommand(t *testing.T) {
	// Gemini is the one WiringHook agent whose config holds a wrapper-script path
	// rather than a `ctx-wire hook <agent>` invocation.
	const scriptBased = "gemini"
	seen := 0
	for _, a := range agentRegistry {
		if a.ProbeKind != WiringHook || a.Name == scriptBased {
			continue
		}
		seen++
		// Exact equality, not containment: on a non-Windows dev machine the
		// command IS the bare form, so a hardcoded "ctx-wire hook <agent>" needle
		// is still contained in it and the drift only shows up on Windows.
		if want := HookNeedle(a.Name); a.ProbeNeedle != want {
			t.Errorf("%s: ProbeNeedle = %q, want HookNeedle(%q) = %q; a literal needle stops matching the absolute-path command written on Windows",
				a.Name, a.ProbeNeedle, a.Name, want)
		}
		if cmd := hookCommand(a.Name); !strings.Contains(cmd, a.ProbeNeedle) {
			t.Errorf("%s: ProbeNeedle %q does not occur in the installed command %q; doctor would report it unwired",
				a.Name, a.ProbeNeedle, cmd)
		}
	}
	if seen == 0 {
		t.Fatal("no hook agents found in the registry: the check is vacuous")
	}
}

// Every Windows branch of the command builder, exercised on any host. The
// platform-gated tests above skip on Unix, so before this table nothing in a
// normal `go test ./...` run touched the absolute path, the quoting, or the
// PowerShell call operator: all three were shipped-and-then-fixed in 0.1.66/67.
func TestHookCommandForCoversEveryPlatformBranch(t *testing.T) {
	const winPlain = `C:\Users\x\AppData\Local\ctx-wire\bin\ctx-wire.exe`
	const winSpaced = `C:\Users\User Name\AppData\Local\ctx-wire\bin\ctx-wire.exe`

	cases := []struct {
		name  string
		goos  string
		exe   string
		agent string
		want  string
	}{
		{"unix stays bare", "darwin", "/usr/local/bin/ctx-wire", "claude", "ctx-wire hook claude"},
		{"linux stays bare", "linux", "/usr/local/bin/ctx-wire", "copilot", "ctx-wire hook copilot"},
		{"windows uses the absolute path", "windows", winPlain, "claude", winPlain + " hook claude"},
		{"windows spaced path is quoted", "windows", winSpaced, "claude", `"` + winSpaced + `" hook claude`},
		{"windows spaced path for a powershell agent gets the call operator", "windows", winSpaced, "copilot", `& "` + winSpaced + `" hook copilot`},
		{"windows unspaced path needs no call operator", "windows", winPlain, "copilot", winPlain + " hook copilot"},
		{"unknown own path falls back to PATH", "windows", "", "copilot", "ctx-wire hook copilot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hookCommandFor(c.goos, c.exe, c.agent)
			if got != c.want {
				t.Errorf("hookCommandFor(%q, %q, %q) = %q, want %q", c.goos, c.exe, c.agent, got, c.want)
			}
			// Whatever form we write, detection and uninstall must recognize it,
			// or init re-adds an entry uninstall cannot remove.
			if !isHookCommand(got, c.agent) {
				t.Errorf("isHookCommand does not recognize %q", got)
			}
			if !strings.Contains(got, HookNeedle(c.agent)) {
				t.Errorf("%q lacks probe needle %q: doctor would report it unwired", got, HookNeedle(c.agent))
			}
		})
	}
}
