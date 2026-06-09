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

// TestShimWalkPreservesArgs exercises the REAL ancestor walk (no force flag, so
// the one-ps parse actually runs) and proves it neither crashes nor clobbers the
// original "$@". With no steering agent in the test's ancestry the shim passes
// through to the real command, which must receive the arguments byte-for-byte,
// spaces and a glob char intact. If the walk had used `set --` (which would
// overwrite the positional params), the real command would get the ps row instead.
func TestShimWalkPreservesArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shim script")
	}
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real 'mytool' that prints each argument it received on its own line.
	writeScript(t, filepath.Join(realDir, "mytool"),
		"#!/bin/sh\nfor a in \"$@\"; do echo \"ARG:$a\"; done\n")
	// A fake ctx-wire: reached only if the shim WIRES (a steering ancestor), which
	// must not happen here.
	fakeCtxWire := filepath.Join(dir, "ctx-wire")
	writeScript(t, fakeCtxWire, "#!/bin/sh\necho WIRED\n")
	writeScript(t, filepath.Join(dir, "mytool"), shimScript("mytool", fakeCtxWire))

	sep := string(os.PathListSeparator)
	// No CTX_WIRE_SHIMS force flag: the ancestor walk runs for real.
	cmd := exec.Command(filepath.Join(dir, "mytool"), "hello world", "*.go", "a=b c")
	cmd.Env = append(shimTestEnv(), "PATH="+dir+sep+realDir+sep+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim run: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "WIRED") {
		t.Fatalf("no steering ancestor, so the shim must pass through, not wire:\n%s", got)
	}
	for _, want := range []string{"ARG:hello world", "ARG:*.go", "ARG:a=b c"} {
		if !strings.Contains(got, want) {
			t.Errorf("real command lost an argument (walk clobbered $@?): want %q in:\n%s", want, got)
		}
	}
}

// TestShimWalkDetectsSteeringAncestor proves the one-ps parse surfaces the
// ancestor's comm+args correctly: a parent process whose argv contains a steering
// agent name (cline) must be detected, so the shim wires and attributes the run.
func TestShimWalkDetectsSteeringAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shim script")
	}
	// The walk matches the ancestor's full ps row (path included), so the test
	// dir must not itself contain an agent name. t.TempDir() can land under an
	// agent-named TMPDIR (e.g. /tmp/claude-*), which would match *claude* first
	// and pass through. Use a neutral /tmp base and skip if it is still tainted.
	dir, err := os.MkdirTemp("/tmp", "cwshim")
	if err != nil {
		t.Skipf("no neutral temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	for _, a := range []string{"claude", "codex", "cursor", "gemini", "copilot", "windsurf", "kilocode", "antigravity", "vscode", "visualstudio", "hermes", "opencode", "agent-browser"} {
		if strings.Contains(dir, a) {
			t.Skipf("temp path %q contains agent name %q; cannot isolate detection", dir, a)
		}
	}
	realDir := filepath.Join(dir, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, filepath.Join(realDir, "git"), "#!/bin/sh\nexit 0\n")
	// fake ctx-wire: reached only when the shim WIRES; echoes the attributed agent.
	fakeCtxWire := filepath.Join(dir, "ctx-wire")
	writeScript(t, fakeCtxWire, "#!/bin/sh\necho \"WIRED-${CTX_WIRE_AGENT:-none}\"\n")
	writeScript(t, filepath.Join(dir, "git"), shimScript("git", fakeCtxWire))

	// A parent whose argv contains a steering agent name. It runs the shim as a
	// CHILD (no exec), so it stays the shim's parent and the walk sees its argv.
	wrapper := filepath.Join(dir, "cline-host.sh")
	writeScript(t, wrapper, "#!/bin/sh\n"+filepath.Join(dir, "git")+" status\n")

	sep := string(os.PathListSeparator)
	cmd := exec.Command(wrapper)
	cmd.Env = append(shimTestEnv(), "PATH="+dir+sep+realDir+sep+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapper run: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "WIRED") {
		t.Fatalf("a cline ancestor must be detected and wired (parse failed to surface comm+args?):\n%s", got)
	}
	if !strings.Contains(got, "cline") {
		t.Errorf("the detected steering agent should be attributed as cline, got %q", got)
	}
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
