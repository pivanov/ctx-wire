package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setConfigPath creates a temp config.toml path, sets the config env var to
// point at it, and returns the path.
func setConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	t.Setenv(envConfig, path)
	return path
}

func TestSetReadCeilingRoundTrips(t *testing.T) {
	path := setConfigPath(t)
	if _, err := SetReadCeiling("measure"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.Hooks.ReadCeiling != "measure" {
		t.Fatalf("after set measure: %+v, %v", cfg.Hooks, err)
	}
	// Write a second key into [hooks] manually, then upsert read_ceiling again.
	// Both keys must survive (no clobbering).
	extra := "[hooks]\nread_ceiling = \"measure\"\nsome_other_key = true\n"
	if err := os.WriteFile(path, []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetReadCeiling("on"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if n := strings.Count(string(data), "read_ceiling"); n != 1 {
		t.Fatalf("want exactly one read_ceiling line, got %d:\n%s", n, data)
	}
	if !strings.Contains(string(data), "some_other_key") {
		t.Fatalf("other hook key was clobbered:\n%s", data)
	}
	if _, err := SetReadCeiling("off"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = Load()
	if cfg.Hooks.ReadCeiling != "off" {
		t.Fatalf("disable failed: %+v", cfg.Hooks)
	}
}
