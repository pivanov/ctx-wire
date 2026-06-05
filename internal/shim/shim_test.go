package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesManagedExecutableShim(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)
	writeRealTool(t, realDir, "git")
	ctxWire := filepath.Join(shimDir, "ctx-wire")
	if err := os.WriteFile(ctxWire, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := Install(shimDir, ctxWire, []string{"git"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(report.Changed) != 1 || report.Changed[0] != "git" {
		t.Fatalf("changed = %#v, want git", report.Changed)
	}
	path := filepath.Join(shimDir, "git")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		marker,
		"CTX_WIRE_DISABLE_SHIMS",
		"CTX_WIRE_AGENT_SHIMS",
		"CTX_WIRE_SHIMS",
		"exec \"$real\" \"$@\"",
		"CTX_WIRE_SHIM=$cmd",
		"export CTX_WIRE_SHIM",
		"command -v \"$cmd\"",
		"exec \"$ctx_wire\" run \"$real\"",
		"is_ctx_wire_shim",
		"CTX_WIRE_SHIM_DEPTH",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("shim missing %q:\n%s", want, text)
		}
	}
	if mode := mustStat(t, path).Mode().Perm(); mode != 0o755 {
		t.Fatalf("mode = %o, want 755", mode)
	}
}

func TestInstallDoesNotOverwriteUserBinary(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)
	writeRealTool(t, realDir, "git")
	ctxWire := filepath.Join(shimDir, "ctx-wire")
	if err := os.WriteFile(ctxWire, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(shimDir, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\necho user\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := Install(shimDir, ctxWire, []string{"git"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0] != "git" {
		t.Fatalf("skipped = %#v, want git", report.Skipped)
	}
	data, err := os.ReadFile(git)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "#!/bin/sh\necho user\n" {
		t.Fatalf("user binary was overwritten:\n%s", data)
	}
}

func TestInspectReportsActiveShim(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)
	writeRealTool(t, realDir, "git")
	ctxWire := filepath.Join(shimDir, "ctx-wire")
	if err := os.WriteFile(ctxWire, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(shimDir, ctxWire, []string{"git"}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	st := Inspect(shimDir, []string{"git"})
	if len(st.Installed) != 1 || st.Installed[0] != "git" {
		t.Fatalf("installed = %#v, want git", st.Installed)
	}
	if len(st.Active) != 1 || st.Active[0] != "git" {
		t.Fatalf("active = %#v, want git", st.Active)
	}
}

func TestInstallSkipsMissingRealCommandAndRemovesStaleShim(t *testing.T) {
	shimDir := t.TempDir()
	ctxWire := filepath.Join(shimDir, "ctx-wire")
	if err := os.WriteFile(ctxWire, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(shimDir, "jq")
	if err := os.WriteFile(stale, []byte("#!/bin/sh\n"+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir)

	report, err := Install(shimDir, ctxWire, []string{"jq"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(report.Missing) != 1 || report.Missing[0] != "jq" {
		t.Fatalf("missing = %#v, want jq", report.Missing)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale shim still exists or stat failed differently: %v", err)
	}
}

func TestUninstallRemovesOnlyManagedShims(t *testing.T) {
	shimDir := t.TempDir()
	managed := filepath.Join(shimDir, "git")
	user := filepath.Join(shimDir, "ls")
	if err := os.WriteFile(managed, []byte(marker+"\nexec ctx-wire\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte("#!/bin/sh\necho user\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := Uninstall(shimDir, []string{"git", "ls", "jq"})
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(report.Removed) != 1 || report.Removed[0] != "git" {
		t.Fatalf("removed = %#v, want git", report.Removed)
	}
	if len(report.Skipped) != 1 || report.Skipped[0] != "ls" {
		t.Fatalf("skipped = %#v, want ls", report.Skipped)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed shim still exists: %v", err)
	}
	data, err := os.ReadFile(user)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "#!/bin/sh\necho user\n" {
		t.Fatalf("user binary changed:\n%s", data)
	}
}

func TestResolveRealSkipsManagedShim(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()
	ctxWire := filepath.Join(shimDir, "ctx-wire")
	if err := os.WriteFile(ctxWire, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(shimDir, ctxWire, []string{"git"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	realGit := filepath.Join(realDir, "git")
	if err := os.WriteFile(realGit, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)

	got, ok := ResolveReal("git")
	if !ok {
		t.Fatal("ResolveReal did not detect the managed shim")
	}
	if cleanPath(got) != cleanPath(realGit) {
		t.Fatalf("ResolveReal = %q, want %q", got, realGit)
	}

	got, ok = ResolveReal(filepath.Join(shimDir, "git"))
	if !ok {
		t.Fatal("ResolveReal absolute shim did not detect the managed shim")
	}
	if cleanPath(got) != cleanPath(realGit) {
		t.Fatalf("ResolveReal absolute = %q, want %q", got, realGit)
	}
}

func TestResolveRealSkipsSecondShimDir(t *testing.T) {
	// Two managed shim dirs on PATH (the classic post-upgrade state) ahead of the
	// real binary. ResolveReal must skip BOTH shim sets and land on the real git,
	// never resolving one shim to the other (which is the fork-bomb condition).
	shimA := t.TempDir()
	shimB := t.TempDir()
	realDir := t.TempDir()
	ctxWire := filepath.Join(shimA, "ctx-wire")
	if err := os.WriteFile(ctxWire, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	realGit := filepath.Join(realDir, "git")
	if err := os.WriteFile(realGit, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", shimA+sep+shimB+sep+realDir)
	if _, err := Install(shimA, ctxWire, []string{"git"}); err != nil {
		t.Fatalf("Install shimA: %v", err)
	}
	if _, err := Install(shimB, ctxWire, []string{"git"}); err != nil {
		t.Fatalf("Install shimB: %v", err)
	}

	got, ok := ResolveReal("git")
	if !ok {
		t.Fatal("ResolveReal did not detect the managed shim")
	}
	if cleanPath(got) != cleanPath(realGit) {
		t.Fatalf("ResolveReal = %q, want real git %q (must skip both shim dirs)", got, realGit)
	}
}

func TestManagedShimDirsAndBinariesOnPATH(t *testing.T) {
	shimA := t.TempDir()
	shimB := t.TempDir()
	realDir := t.TempDir()
	ctxWire := filepath.Join(shimA, "ctx-wire")
	if err := os.WriteFile(ctxWire, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A second ctx-wire binary in shimB simulates a stale install.
	if err := os.WriteFile(filepath.Join(shimB, "ctx-wire"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "git"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", shimA+sep+shimB+sep+realDir)
	if _, err := Install(shimA, ctxWire, []string{"git"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(shimB, ctxWire, []string{"git"}); err != nil {
		t.Fatal(err)
	}

	if dirs := ManagedShimDirsOnPATH(); len(dirs) != 2 {
		t.Fatalf("ManagedShimDirsOnPATH = %v, want 2 dirs", dirs)
	}
	if bins := CtxWireBinariesOnPATH(); len(bins) != 2 {
		t.Fatalf("CtxWireBinariesOnPATH = %v, want 2 binaries", bins)
	}
}

func TestResolveRealLeavesNormalCommandAlone(t *testing.T) {
	dir := t.TempDir()
	realGit := filepath.Join(dir, "git")
	if err := os.WriteFile(realGit, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	got, ok := ResolveReal("git")
	if ok {
		t.Fatal("ResolveReal treated a normal binary as a shim")
	}
	if got != "git" {
		t.Fatalf("ResolveReal = %q, want original name", got)
	}
}

func TestRecordUseAndSummary(t *testing.T) {
	log := filepath.Join(t.TempDir(), "shims.jsonl")
	t.Setenv(EnvLog, log)

	if err := RecordUse("git", "git status --short"); err != nil {
		t.Fatalf("RecordUse: %v", err)
	}
	if err := RecordUse("rg", "rg [REDACTED]"); err != nil {
		t.Fatalf("RecordUse second: %v", err)
	}
	count, last, err := UsageSummary()
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if last == "" {
		t.Fatal("last use timestamp is empty")
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "git status --short") || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("log did not contain scrubbed commands:\n%s", data)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func writeRealTool(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
