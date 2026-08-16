package install

import (
	"runtime"
	"strings"
	"testing"
)

// A hook entry ctx-wire writes must always be recognized by the code that
// detects and removes it. If these ever disagree, `init` writes an entry that
// `uninstall` cannot find, leaving a dead hook behind that denies tool calls.
func TestHookCommandRoundTrips(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "cursor", "copilot"} {
		cmd := hookCommand(agent)
		if !isHookCommand(cmd, agent) {
			t.Errorf("hookCommand(%q) = %q, which isHookCommand does not recognize", agent, cmd)
		}
		if !strings.Contains(cmd, hookNeedle(agent)) {
			t.Errorf("hookCommand(%q) = %q, missing probe needle %q (doctor would report it unwired)",
				agent, cmd, hookNeedle(agent))
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
		{"quoted windows path with spaces", `"C:\Users\Ivan Mitev\AppData\Local\ctx-wire\bin\ctx-wire.exe" hook claude`, true},
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
