package shim

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolveRealExe checks the shim-mode resolver finds the real binary while
// skipping ctx-wire shims, and errors (rather than returning a shim, which would
// recurse) when only shims are on PATH. It runs on every OS: the shim is named
// by shimFileName (git on Unix, git.cmd on Windows) and the real binary by the
// platform's executable extension.
func TestResolveRealExe(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()
	writeExecFile(t, filepath.Join(shimDir, shimFileName("git")), "@echo off\nrem "+marker+"\n")

	realName := "git"
	if runtime.GOOS == "windows" {
		realName = "git.exe"
	}
	writeExecFile(t, filepath.Join(realDir, realName), "binary\n")

	old := os.Getenv("PATH")
	defer os.Setenv("PATH", old)
	sep := string(os.PathListSeparator)

	// Shim first, real second: must skip the shim and resolve the real binary.
	os.Setenv("PATH", shimDir+sep+realDir)
	got, err := ResolveRealExe("git")
	if err != nil {
		t.Fatalf("ResolveRealExe: %v", err)
	}
	if filepath.Dir(got) != realDir {
		t.Errorf("resolved %q, want the real binary in %q", got, realDir)
	}

	// Only the shim on PATH: must error, never hand back the shim itself.
	os.Setenv("PATH", shimDir)
	if _, err := ResolveRealExe("git"); err == nil {
		t.Error("expected an error when only ctx-wire shims are on PATH")
	}
}

func writeExecFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
