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
	for _, want := range []string{
		"*claude*) should_wire=1; detected_agent=claude;",
		"*codex*) should_wire=1; detected_agent=codex;",
		"*cursor*) should_wire=1; detected_agent=cursor;",
		"*gemini*) should_wire=1; detected_agent=gemini;",
		"*copilot*) should_wire=1; detected_agent=copilot;",
		`if [ -z "${CTX_WIRE_AGENT:-}" ] && [ -n "${detected_agent:-}" ]; then`,
		"export CTX_WIRE_AGENT",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("shim script missing %q:\n%s", want, script)
		}
	}
	// agent-browser is wired but not attributed (it is a tool, not an agent).
	if strings.Contains(script, "detected_agent=agent-browser") {
		t.Fatalf("agent-browser should wire without being attributed as an agent:\n%s", script)
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
	cmd.Env = append(os.Environ(),
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

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
