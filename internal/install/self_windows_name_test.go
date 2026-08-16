package install

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A Windows executable must be named .exe or it cannot be launched by name:
// CreateProcess resolves a bare command through PATHEXT and never matches an
// extensionless file. `ctx-wire init` self-installs through SelfInstallPath, so
// getting this wrong drops a dead binary onto the user's PATH.
func TestSelfInstallPathHasPlatformExtension(t *testing.T) {
	got, err := SelfInstallPath()
	if err != nil {
		t.Fatalf("SelfInstallPath: %v", err)
	}
	base := filepath.Base(got)
	if runtime.GOOS == "windows" {
		if base != "ctx-wire.exe" {
			t.Errorf("SelfInstallPath base = %q, want ctx-wire.exe (extensionless is not executable on Windows)", base)
		}
		return
	}
	if base != "ctx-wire" {
		t.Errorf("SelfInstallPath base = %q, want ctx-wire on %s", base, runtime.GOOS)
	}
}

// The legacy path exists only to clean up the dead copy older Windows builds
// wrote. It must never collide with the current path, or uninstall would delete
// the working binary while reporting it as stale.
func TestLegacySelfInstallPathOnlyOnWindows(t *testing.T) {
	legacy, err := LegacySelfInstallPath()
	if err != nil {
		t.Fatalf("LegacySelfInstallPath: %v", err)
	}
	if runtime.GOOS != "windows" {
		if legacy != "" {
			t.Errorf("LegacySelfInstallPath on %s = %q, want empty", runtime.GOOS, legacy)
		}
		return
	}
	current, err := SelfInstallPath()
	if err != nil {
		t.Fatalf("SelfInstallPath: %v", err)
	}
	if legacy == current {
		t.Fatal("legacy path equals the current path; uninstall would remove the live binary")
	}
	if !strings.HasSuffix(legacy, "ctx-wire") {
		t.Errorf("legacy path = %q, want the extensionless form", legacy)
	}
}
