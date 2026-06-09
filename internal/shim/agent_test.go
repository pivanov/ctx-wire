package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShimScriptDetectsAndExportsAgent asserts the generated Unix shim maps each
// recognized parent process to a CTX_WIRE_AGENT value and exports it only when
// an outer hook has not already set one.
func TestShimScriptDetectsAndExportsAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shim script")
	}
	script := shimScript("git", "/usr/local/bin/ctx-wire")

	// Steering-only / opt-in MCP agents still wire and attribute: the shim is
	// their only coverage.
	for _, want := range []string{
		"*windsurf*) should_wire=1; detected_agent=windsurf;",
		"*cline*) should_wire=1; detected_agent=cline;",
		"*kilocode*) should_wire=1; detected_agent=kilocode;",
		"*antigravity*) should_wire=1; detected_agent=antigravity;",
		`*vscode*|*"Visual Studio Code"*|*"visual studio code"*) should_wire=1; detected_agent=vscode;`,
		`*visualstudio*|*"Visual Studio"*|*"visual studio"*) should_wire=1; detected_agent=visualstudio;`,
		`if [ -z "${CTX_WIRE_AGENT:-}" ] && [ -n "${detected_agent:-}" ]; then`,
		"export CTX_WIRE_AGENT",
		// Hook-capable agents pass through, both as a detected ancestor and as an
		// inherited CTX_WIRE_AGENT.
		`*claude*|*codex*|*cursor*|*gemini*|*copilot*|*opencode*|*pi-coding-agent*|*"pi coding agent"*|*/.pi/agent*|*hermes*)`,
		"claude|codex|cursor|gemini|copilot|opencode|pi|hermes)",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("shim script missing %q:\n%s", want, script)
		}
	}

	// Hook/plugin-capable agents must NOT be wired (no attribution line): they are
	// covered by their own rewrite, so wiring would re-introduce $() corruption.
	for _, a := range []string{"claude", "codex", "cursor", "gemini", "copilot", "opencode", "pi", "hermes"} {
		if strings.Contains(script, "detected_agent="+a) {
			t.Fatalf("hook-capable agent %q must pass through, not wire (found detected_agent=%s):\n%s", a, a, script)
		}
	}

	assertInOrder(t, script, []string{
		"*windsurf*) should_wire=1; detected_agent=windsurf;",
		"*cline*) should_wire=1; detected_agent=cline;",
		"*kilocode*) should_wire=1; detected_agent=kilocode;",
		"*antigravity*) should_wire=1; detected_agent=antigravity;",
		`*vscode*|*"Visual Studio Code"*|*"visual studio code"*) should_wire=1; detected_agent=vscode;`,
		`*visualstudio*|*"Visual Studio"*|*"visual studio"*) should_wire=1; detected_agent=visualstudio;`,
	})
	// agent-browser is wired but not attributed (it is a tool, not an agent).
	if strings.Contains(script, "detected_agent=agent-browser") {
		t.Fatalf("agent-browser should wire without being attributed as an agent:\n%s", script)
	}
}

func assertInOrder(t *testing.T, haystack string, needles []string) {
	t.Helper()
	offset := 0
	for _, needle := range needles {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			t.Fatalf("expected %q after byte %d in shim script", needle, offset)
		}
		offset += idx + len(needle)
	}
}

// TestShimPreservesOuterAgent runs the generated shim against a fake ctx-wire and
// confirms a CTX_WIRE_AGENT already present in the environment (set by an outer
// hook) is passed through rather than overwritten.
func TestShimPreservesOuterAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shim script")
	}
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real `git` the shim resolves to after removing its own dir from PATH.
	writeScript(t, filepath.Join(realDir, "git"), "#!/bin/sh\nexit 0\n")
	// A fake ctx-wire that prints the agent it was invoked with.
	fakeCtxWire := filepath.Join(dir, "ctx-wire")
	writeScript(t, fakeCtxWire, "#!/bin/sh\necho \"AGENT=${CTX_WIRE_AGENT:-none}\"\n")

	shimPath := filepath.Join(dir, "git")
	writeScript(t, shimPath, shimScript("git", fakeCtxWire))

	// Keep the system PATH so the shim's helpers (dirname, ps, tr) resolve, with
	// the shim and real dirs ahead of it.
	sep := string(os.PathListSeparator)
	cmd := exec.Command(shimPath, "status")
	cmd.Env = append(shimTestEnv(),
		"PATH="+dir+sep+realDir+sep+os.Getenv("PATH"),
		"CTX_WIRE_SHIMS=1",     // force wiring without a recognized parent
		"CTX_WIRE_AGENT=codex", // outer hook already attributed this run
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "AGENT=codex" {
		t.Fatalf("shim output = %q, want AGENT=codex (outer agent preserved)", got)
	}
}

// shimTestEnv returns os.Environ() with any CTX_WIRE_* variable removed, so the
// shim run is hermetic even when the suite itself is invoked through a ctx-wire
// dogfood wrapper (which sets CTX_WIRE_DISABLE_SHIMS=1 for its children, and
// would otherwise make the shim bail before the agent-export logic under test).
func shimTestEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "CTX_WIRE_") {
			continue
		}
		env = append(env, e)
	}
	return env
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
