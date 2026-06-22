package config

import (
	"os"
	"testing"
)

func TestStripStacktracesDefaultOn(t *testing.T) {
	// No config -> ON by default.
	setConfigPath(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Output.StripStacktracesOn() {
		t.Fatalf("missing config: strip_stacktraces should default ON, got %v", cfg.Output.StripStacktraces)
	}
	// Explicit false -> OFF (opt-out honored).
	path := setConfigPath(t)
	if err := os.WriteFile(path, []byte("[output]\nstrip_stacktraces = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.StripStacktracesOn() {
		t.Fatal("explicit strip_stacktraces=false should disable it")
	}
}
