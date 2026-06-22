package config

import (
	"os"
	"testing"
)

func TestDedupDefaultOn(t *testing.T) {
	// No config file at all -> dedup ON by default.
	setConfigPath(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Dedup.On() {
		t.Fatalf("missing config: dedup should default ON, got Enabled=%v", cfg.Dedup.Enabled)
	}

	// Explicit enabled = false -> OFF (the opt-out is honored).
	path := setConfigPath(t)
	if err := os.WriteFile(path, []byte("[dedup]\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dedup.On() {
		t.Fatal("explicit enabled=false should disable dedup")
	}

	// Explicit enabled = true -> ON.
	if err := os.WriteFile(path, []byte("[dedup]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Dedup.On() {
		t.Fatal("explicit enabled=true should enable dedup")
	}
}
