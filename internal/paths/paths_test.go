package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDataHomeXDGWins(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	got, err := DataHome()
	if err != nil || got != "/custom/data" {
		t.Fatalf("DataHome = (%q, %v), want /custom/data", got, err)
	}
}

func TestConfigHomeXDGWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/cfg")
	got, err := ConfigHome()
	if err != nil || got != "/custom/cfg" {
		t.Fatalf("ConfigHome = (%q, %v), want /custom/cfg", got, err)
	}
}

func TestHomeFallbacks(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if runtime.GOOS == "windows" {
		t.Skip("fallback is %LOCALAPPDATA%/%APPDATA% on Windows")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := DataHome(); got != filepath.Join(home, ".local", "share") {
		t.Errorf("DataHome fallback = %q", got)
	}
	if got, _ := ConfigHome(); got != filepath.Join(home, ".config") {
		t.Errorf("ConfigHome fallback = %q", got)
	}
}
