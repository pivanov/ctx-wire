package main

import (
	"path/filepath"
	"testing"

	"ctx-wire/internal/telemetry"
)

func TestTelemetryDisableDropsOnlyCommandBreakdown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TELEMETRY_CONFIG", filepath.Join(dir, "telemetry.json"))
	t.Setenv("CTX_WIRE_TELEMETRY_STATE", filepath.Join(dir, "state.json"))

	if code := cmdTelemetry([]string{"disable"}); code != 0 {
		t.Fatalf("telemetry disable exit = %d, want 0", code)
	}
	status, err := telemetry.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Enabled {
		t.Fatal("aggregate telemetry must stay enabled")
	}
	if status.ShareImprovements {
		t.Fatal("telemetry disable must drop only the command breakdown")
	}

	if code := cmdTelemetry([]string{"enable"}); code != 0 {
		t.Fatalf("telemetry enable exit = %d, want 0", code)
	}
	status, err = telemetry.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus after enable: %v", err)
	}
	if !status.Enabled || !status.ShareImprovements {
		t.Fatalf("status after enable = %#v, want aggregate + breakdown enabled", status)
	}
}

func TestTelemetryImprovementsCommandRemoved(t *testing.T) {
	if code := cmdTelemetry([]string{"improvements", "off"}); code != 2 {
		t.Fatalf("removed telemetry subcommand exit = %d, want 2", code)
	}
}
