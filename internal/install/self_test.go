package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallSelfCopiesExecutable(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ctx-wire-src")
	dest := filepath.Join(dir, ".local", "bin", "ctx-wire")
	if err := os.WriteFile(source, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := InstallSelf(source, dest)
	if err != nil {
		t.Fatalf("InstallSelf: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("copied content = %q", got)
	}
	if mode := mustStat(t, dest).Mode().Perm(); mode != 0o755 {
		t.Fatalf("mode = %o, want 755", mode)
	}
}

func TestInstallSelfIdempotentWhenContentAndModeMatch(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ctx-wire-src")
	dest := filepath.Join(dir, ".local", "bin", "ctx-wire")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSelf(source, dest); err != nil {
		t.Fatalf("first install: %v", err)
	}

	changed, err := InstallSelf(source, dest)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false")
	}
}

func TestInstallSelfFixesModeWhenContentMatches(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ctx-wire-src")
	dest := filepath.Join(dir, ".local", "bin", "ctx-wire")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := InstallSelf(source, dest)
	if err != nil {
		t.Fatalf("InstallSelf: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true for chmod")
	}
	if mode := mustStat(t, dest).Mode().Perm(); mode != 0o755 {
		t.Fatalf("mode = %o, want 755", mode)
	}
}

func TestUninstallSelfRemovesBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ctx-wire")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	removed, err := UninstallSelf(path)
	if err != nil {
		t.Fatalf("UninstallSelf: %v", err)
	}
	if !removed {
		t.Fatal("expected removed=true")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("binary still exists: %v", err)
	}
}

func TestUninstallSelfMissingIsNoop(t *testing.T) {
	removed, err := UninstallSelf(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("UninstallSelf: %v", err)
	}
	if removed {
		t.Fatal("expected removed=false for missing file")
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
