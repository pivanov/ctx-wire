package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Unix shim must not depend on an external dirname(1): a hostile or stripped
// PATH (missing /usr/bin) made the old form abort at "line 5: dirname: command
// not found". It must compute its own directory with shell builtins only.
func TestShimTemplateNoDirnameDependency(t *testing.T) {
	s := shimScript("git", "/opt/ctx-wire/ctx-wire")
	if strings.Contains(s, "dirname") {
		t.Errorf("shim still calls external dirname:\n%s", s)
	}
	if !strings.Contains(s, "shim_dir=${0%/*}") {
		t.Errorf("shim does not compute its dir via parameter expansion ${0%%/*}:\n%s", s)
	}
	// The template is a fmt.Sprintf format string; a stray %% would leak into the
	// rendered shell. The rendered output must carry a single %.
	if strings.Contains(s, "${0%%/*}") {
		t.Errorf("rendered shim leaked a doubled %%%% (Sprintf escaping bug):\n%s", s)
	}
}

// cat is the highest-volume command but also a transport host shells use under
// command substitution; filtering it there silently corrupts captures, so it
// must not be a default shim. Removing it must not silently strand existing
// users, so it must be in DeprecatedShims for the self-heal to prune.
func TestCatIsDeShimmedButDeprecation(t *testing.T) {
	for _, c := range DefaultCommands {
		if c == "cat" {
			t.Fatal("cat must not be in DefaultCommands (it corrupts command-substitution captures)")
		}
	}
	found := false
	for _, c := range DeprecatedShims {
		if c == "cat" {
			found = true
		}
	}
	if !found {
		t.Fatal("cat must be in DeprecatedShims so the self-heal prunes existing cat shims")
	}
}

// grep/head/sed/tail are commonly used in pipes and redirections; shim-side
// filtering silently truncates/corrupts redirected data (same bug class as cat).
// They must not be in DefaultCommands. They must be in DeprecatedShims so the
// self-heal prunes shims left by older installs. cat must still be there too.
func TestPipeCorruptionCommandsDeShimmed(t *testing.T) {
	deShimmed := []string{"grep", "head", "sed", "tail"}

	defaultSet := make(map[string]bool, len(DefaultCommands))
	for _, c := range DefaultCommands {
		defaultSet[c] = true
	}
	deprecatedSet := make(map[string]bool, len(DeprecatedShims))
	for _, c := range DeprecatedShims {
		deprecatedSet[c] = true
	}

	for _, cmd := range deShimmed {
		if defaultSet[cmd] {
			t.Errorf("%s must NOT be in DefaultCommands (shim-side filtering corrupts piped/redirected output)", cmd)
		}
		if !deprecatedSet[cmd] {
			t.Errorf("%s must be in DeprecatedShims so the self-heal prunes existing shims", cmd)
		}
	}

	// Regression: cat must still be in DeprecatedShims.
	if !deprecatedSet["cat"] {
		t.Error("cat must still be in DeprecatedShims (regression guard)")
	}
}

// RefreshManaged must prune a managed shim for a deprecated command from every
// managed dir on PATH (an upgrade has to reach existing users), while keeping
// non-deprecated managed shims and never touching foreign files.
func TestRefreshManagedPrunesDeprecatedShim(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	ctxWire := filepath.Join(t.TempDir(), "ctx-wire")
	absCtxWire, _ := filepath.Abs(ctxWire)

	write := func(name, content string) string {
		p := filepath.Join(dir, shimFileName(name))
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	catShim := write("cat", shimScript("cat", absCtxWire)) // managed, deprecated -> prune
	gitShim := write("git", shimScript("git", absCtxWire)) // managed, kept
	foreignBody := "#!/bin/sh\necho not ours\n"
	foreign := write("dd", foreignBody) // not ours -> never touched

	RefreshManaged(ctxWire)

	if _, err := os.Stat(catShim); !os.IsNotExist(err) {
		t.Errorf("deprecated cat shim was not pruned (err=%v)", err)
	}
	if _, err := os.Stat(gitShim); err != nil {
		t.Errorf("non-deprecated git shim must be kept: %v", err)
	}
	if got, _ := os.ReadFile(foreign); string(got) != foreignBody {
		t.Error("a foreign (non-ctx-wire) file must never be removed or modified")
	}
}

// A PATH dir that holds ONLY a deprecated managed shim (no current-default shim)
// must still be discovered and pruned. Regression for ManagedShimDirsOnPATH
// keying discovery off DefaultCommands alone, which would skip such a dir.
func TestRefreshManagedPrunesCatOnlyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	ctxWire := filepath.Join(t.TempDir(), "ctx-wire")
	absCtxWire, _ := filepath.Abs(ctxWire)

	catShim := filepath.Join(dir, shimFileName("cat"))
	if err := os.WriteFile(catShim, []byte(shimScript("cat", absCtxWire)), 0o755); err != nil {
		t.Fatal(err)
	}

	RefreshManaged(ctxWire)

	if _, err := os.Stat(catShim); !os.IsNotExist(err) {
		t.Errorf("a cat-only managed dir must still be discovered and pruned (err=%v)", err)
	}
}

// ResolveRealStrict must error only when a command resolves ONLY to a ctx-wire
// shim with no real binary, so the runner fails clean instead of re-executing
// the shim. Real binaries and absolute paths must pass through unchanged.
func TestResolveRealStrict(t *testing.T) {
	shimDir := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+binDir)

	managed := "#!/bin/sh\n" + marker + "\nexec true\n"
	realBody := "#!/bin/sh\necho real\n"
	mustWrite := func(d, name, body string) string {
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// only a shim, no real binary anywhere -> error (would otherwise re-enter)
	mustWrite(shimDir, "onlyshim", managed)
	// a shim in front of a real binary -> resolves to the real, no error
	mustWrite(shimDir, "dual", managed)
	realDual := mustWrite(binDir, "dual", realBody)
	// a plain real binary, never shimmed -> passthrough
	realPlain := mustWrite(binDir, "plain", realBody)

	if _, err := ResolveRealStrict("onlyshim"); err == nil {
		t.Error("onlyshim resolves only to a shim; ResolveRealStrict must error, not re-enter")
	}
	if got, err := ResolveRealStrict("dual"); err != nil || got != realDual {
		t.Errorf("dual must resolve to the real binary %q, got %q err=%v", realDual, got, err)
	}
	if _, err := ResolveRealStrict("plain"); err != nil {
		t.Errorf("plain real command must pass through, got err=%v", err)
	}
	// absolute path to a real binary (what the shim hands `ctx-wire run`) must pass
	if got, err := ResolveRealStrict(realPlain); err != nil || got != realPlain {
		t.Errorf("absolute real path must pass through unchanged, got %q err=%v", got, err)
	}
	// a command that exists nowhere is not our error to raise; let exec surface it
	if _, err := ResolveRealStrict("ghost-cmd-xyz"); err != nil {
		t.Errorf("missing command must not be reported as a shim-only error, got %v", err)
	}
}
