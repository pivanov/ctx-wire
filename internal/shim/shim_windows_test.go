//go:build windows

package shim

import (
	"strings"
	"testing"
)

// TestWindowsShimTemplate pins the .cmd shim contract on Windows: it delegates to
// `run --shim`, propagates the exit code, and keeps the marker on line 2 so
// install/uninstall scanning (isManaged / isManagedShimFile) still recognizes it.
func TestWindowsShimTemplate(t *testing.T) {
	s := shimScript("git", `C:\Users\me\.local\bin\ctx-wire.exe`)

	if !strings.HasPrefix(s, "@echo off") {
		t.Errorf("template should start with @echo off, got:\n%s", s)
	}
	if !strings.Contains(s, "run --shim git") {
		t.Error("template should invoke `run --shim git`")
	}
	if !strings.Contains(s, "exit /b %errorlevel%") {
		t.Error("template should propagate the exit code via exit /b")
	}
	if !isManaged([]byte(s)) {
		t.Error("template should be recognized as a managed shim (marker missing)")
	}

	lines := strings.Split(s, "\r\n")
	if len(lines) < 2 || !strings.Contains(lines[1], marker) {
		t.Errorf("marker must be on line 2 (isManagedShimFile reads a prefix); lines=%q", lines)
	}
}
