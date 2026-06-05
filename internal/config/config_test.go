package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExcludeCommands(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[hooks]\nexclude_commands = [\"curl\", \"playwright\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envConfig, p)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.Hooks.ExcludeCommands; len(got) != 2 || got[0] != "curl" || got[1] != "playwright" {
		t.Fatalf("exclude_commands = %v", got)
	}
}

func TestLoadOutputSection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[output]\nultra_compact = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envConfig, p)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Output.UltraCompact {
		t.Fatal("expected output.ultra_compact = true")
	}
}

func TestLoadMissingFileIsZero(t *testing.T) {
	t.Setenv(envConfig, filepath.Join(t.TempDir(), "nope.toml"))
	c, err := Load()
	if err != nil {
		t.Fatalf("missing config should not error, got %v", err)
	}
	if len(c.Hooks.ExcludeCommands) != 0 {
		t.Fatalf("expected zero config, got %+v", c)
	}
}

func TestLoadMalformedErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("this is = not = valid = toml ["), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envConfig, p)
	if _, err := Load(); err == nil {
		t.Fatal("malformed config should return an error")
	}
}
