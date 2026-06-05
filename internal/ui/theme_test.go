package ui

import (
	"strings"
	"testing"
)

func TestHeadingPlain(t *testing.T) {
	got := Plain().Heading("ctx-wire gain")
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("Heading should be title + rule, got %d lines: %q", len(lines), got)
	}
	if lines[0] != "ctx-wire gain" {
		t.Errorf("title line = %q, want %q", lines[0], "ctx-wire gain")
	}
	if want := strings.Repeat("─", HeadingWidth); lines[1] != want {
		t.Errorf("rule line = %q, want a %d-wide rule", lines[1], HeadingWidth)
	}
}

func TestFieldPlain(t *testing.T) {
	// Label is padded to the shared column and gets a colon if missing.
	got := Plain().Field("Telemetry", "enabled")
	if want := "Telemetry:" + strings.Repeat(" ", fieldLabelWidth-len("Telemetry:")) + " enabled"; got != want {
		t.Errorf("Field = %q, want %q", got, want)
	}
	// An explicit colon is not doubled.
	if got := Plain().Field("State:", "/tmp/x"); !strings.HasPrefix(got, "State: ") || strings.Contains(got, "State::") {
		t.Errorf("Field with explicit colon = %q", got)
	}
}
