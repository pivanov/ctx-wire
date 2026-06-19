package main

import (
	"os"
	"strings"
	"testing"
)

func TestSuggestCommand(t *testing.T) {
	if got := suggestCommand("tume"); got != "tune" {
		t.Fatalf("suggestCommand(tume) = %q, want tune", got)
	}
	if got := suggestCommand("completely-different"); got != "" {
		t.Fatalf("suggestCommand(completely-different) = %q, want empty", got)
	}
}

func TestSubcommandHelpGuards(t *testing.T) {
	tests := []struct {
		name string
		fn   func([]string) int
	}{
		{"explain", cmdExplain},
		{"fetch", cmdFetch},
		{"run", cmdRun},
		{"mcp", cmdMCP},
		{"hook", cmdHook},
		{"rewrite", cmdRewrite},
		{"init", cmdInit},
		{"uninstall", cmdUninstall},
		{"trust", cmdTrust},
		{"gain", cmdGain},
		{"verify", cmdVerify},
		{"tune", cmdTune},
		{"telemetry", cmdTelemetry},
		{"discover", cmdDiscover},
		{"doctor", cmdDoctor},
	}
	for _, tt := range tests {
		if code := tt.fn([]string{"--help"}); code != 0 {
			t.Fatalf("%s --help exit = %d, want 0", tt.name, code)
		}
	}
}

func TestInitRequiresTarget(t *testing.T) {
	if code := cmdInit(nil); code != 2 {
		t.Fatalf("cmdInit(nil) exit = %d, want 2", code)
	}
}

func TestTopLevelUsageUsesThemeWhenColorForced(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	f, err := os.CreateTemp(t.TempDir(), "usage-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	usage(f)
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("forced-color usage should contain ANSI escapes:\n%q", out)
	}
	for _, want := range []string{"ctx-wire <command> [args]", "daily:", "diagnose:", "manage:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "manual wrapper used by hooks/shims") || !strings.Contains(out, "debug the shell rewrite") {
		t.Fatalf("usage missing expected text:\n%s", out)
	}
}
