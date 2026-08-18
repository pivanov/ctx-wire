package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyCopilotHookFile renders a hook file in the shape shipped versions wrote,
// so retirement and uninstall are tested against what is actually on disk. The
// event key is PascalCase because that is the only form ctx-wire ever wrote.
func legacyCopilotHookFile(command string) string {
	cmd, err := json.Marshal(command)
	if err != nil {
		panic(err)
	}
	return `{
  "hooks": {
    "PreToolUse": [
      {
        "type": "command",
        "command": ` + string(cmd) + `,
        "cwd": ".",
        "timeout": 5
      }
    ]
  }
}
`
}

// ctx-wire must not create .github/hooks/ctx-wire-rewrite.json any more: it is a
// committed file, so it published a machine-local tool as team-wide config, and
// a teammate or cloud agent without ctx-wire got a non-zero preToolUse result,
// which fails closed and denies their shell tools.
func TestInstallCopilotDoesNotCreateRepoHookFile(t *testing.T) {
	dir := t.TempDir()
	instr := filepath.Join(dir, "copilot-instructions.md")
	hook := filepath.Join(dir, "hooks", "ctx-wire-rewrite.json")

	if _, err := InstallCopilot(instr, hook); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(hook); err == nil {
		t.Error("install wrote the repo hook file; it is committed and denies shell tools for anyone without ctx-wire")
	}
	if _, err := os.Stat(instr); err != nil {
		t.Errorf("install did not write the instructions file: %v", err)
	}
}

// Retiring is part of installing: an already-wired repo must lose the hazard on
// upgrade, not keep it until someone runs uninstall.
func TestInstallCopilotRetiresAnExistingRepoHookFile(t *testing.T) {
	dir := t.TempDir()
	instr := filepath.Join(dir, "copilot-instructions.md")
	hook := filepath.Join(dir, "hooks", "ctx-wire-rewrite.json")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte(legacyCopilotHookFile("ctx-wire hook copilot")), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := InstallCopilot(instr, hook)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !changed {
		t.Error("install reported no change while retiring a hook file")
	}
	if _, err := os.Stat(hook); err == nil {
		t.Error("a previously installed repo hook file survived the upgrade")
	}
}

// A hook file the user added their own entries to is pruned, never deleted.
func TestInstallCopilotPrunesMixedRepoHookFile(t *testing.T) {
	dir := t.TempDir()
	instr := filepath.Join(dir, "copilot-instructions.md")
	hook := filepath.Join(dir, "hooks", "ctx-wire-rewrite.json")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	mixed := `{"hooks":{"PreToolUse":[` +
		`{"type":"command","command":"other-tool guard"},` +
		`{"type":"command","command":"ctx-wire hook copilot"}]}}`
	if err := os.WriteFile(hook, []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallCopilot(instr, hook); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("mixed hook file was deleted instead of pruned: %v", err)
	}
	if !strings.Contains(string(data), "other-tool guard") {
		t.Errorf("foreign hook entry lost:\n%s", data)
	}
	if strings.Contains(string(data), HookNeedle("copilot")) {
		t.Errorf("ctx-wire entry survived:\n%s", data)
	}
}

// A hook file ctx-wire wrote must be removed outright whatever command form it
// holds. Deciding that by byte-comparing against one canonical string broke the
// moment the command became platform-dependent: the file then fell through to
// entry-removal, which leaves a .bak behind in the user's committed .github dir.
func TestUninstallCopilotHookRemovesEveryManagedForm(t *testing.T) {
	for _, cmd := range []string{
		"ctx-wire hook copilot",
		`C:\Users\x\AppData\Local\ctx-wire\bin\ctx-wire.exe hook copilot`,
		`& "C:\Users\User Name\AppData\Local\ctx-wire\bin\ctx-wire.exe" hook copilot`,
	} {
		t.Run(cmd, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ctx-wire-rewrite.json")
			if err := os.WriteFile(path, []byte(legacyCopilotHookFile(cmd)), 0o644); err != nil {
				t.Fatal(err)
			}
			removed, err := UninstallCopilotHook(path)
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if !removed {
				t.Fatal("uninstall reported no change for a fully managed hook file")
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("managed hook file still present after uninstall")
			}
			if _, err := os.Stat(path + ".bak"); err == nil {
				t.Error("uninstall left a .bak beside a file it fully owned")
			}
		})
	}
}

// A hook file the user added to keeps its own entries; only ours is removed.
func TestUninstallCopilotHookPreservesForeignEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ctx-wire-rewrite.json")
	mixed := `{"hooks":{"PreToolUse":[` +
		`{"type":"command","command":"other-tool guard"},` +
		`{"type":"command","command":"ctx-wire hook copilot"}]}}`
	if err := os.WriteFile(path, []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallCopilotHook(path); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("mixed hook file was removed instead of pruned: %v", err)
	}
	if !strings.Contains(string(data), "other-tool guard") {
		t.Errorf("foreign hook entry lost:\n%s", data)
	}
	if strings.Contains(string(data), "hook copilot") {
		t.Errorf("ctx-wire entry survived:\n%s", data)
	}
}

// A file that accumulated one entry under each event spelling (an old install
// plus a newer one) must come out with NEITHER left behind. Returning on the
// first key that changed kept the second.
func TestUninstallCopilotHookSweepsBothEventKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ctx-wire-rewrite.json")
	both := `{"hooks":{` +
		`"preToolUse":[{"type":"command","command":"ctx-wire hook copilot"}],` +
		`"PreToolUse":[{"type":"command","command":"ctx-wire hook copilot"}]}}`
	if err := os.WriteFile(path, []byte(both), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallCopilotHook(path); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return // whole file removed: both entries gone, which is the goal
	}
	if strings.Contains(string(data), HookNeedle("copilot")) {
		t.Errorf("a ctx-wire hook survived uninstall:\n%s", data)
	}
}
