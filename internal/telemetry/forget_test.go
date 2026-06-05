package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForget(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "telemetry.json")
	st := filepath.Join(dir, "telemetry-state.json")
	t.Setenv(envConfig, cfg)
	t.Setenv(envState, st)
	t.Setenv(envEnabled, "") // don't let an env override mask the config

	// Seed both files: enable telemetry (writes config) and leave some state.
	if err := SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := os.WriteFile(st, []byte(`{"pending":{"commands":3}}`), 0o600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("config should exist before forget: %v", err)
	}

	if err := Forget(); err != nil {
		t.Fatalf("Forget: %v", err)
	}

	// The pending/last-reported state is erased.
	if _, err := os.Stat(st); !os.IsNotExist(err) {
		t.Errorf("state should be gone after Forget, stat err = %v", err)
	}
	// The crucial guarantee: telemetry stays DISABLED after withdrawal. Because
	// telemetry is opt-out, deleting the config would silently re-enable it, so
	// Forget must persist a disabled consent instead.
	status, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Enabled {
		t.Error("telemetry must remain disabled after Forget (withdrawal must stick)")
	}

	// Forget is idempotent: running again is not an error and stays disabled.
	if err := Forget(); err != nil {
		t.Errorf("second Forget should be a no-op, got %v", err)
	}
	if status2, err := GetStatus(); err != nil || status2.Enabled {
		t.Errorf("telemetry still enabled after second Forget (err=%v)", err)
	}
}

// TestForgetBeatsDefaultOptOut is the focused regression for the privacy bug:
// with no env override, GetStatus reports enabled by default, but after Forget
// it must report disabled.
func TestForgetBeatsDefaultOptOut(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "telemetry-state.json"))
	t.Setenv(envEnabled, "") // no override: exercise the opt-out default

	if status, err := GetStatus(); err != nil || !status.Enabled {
		t.Fatalf("precondition: telemetry should be enabled by default (err=%v, enabled=%v)", err, status.Enabled)
	}
	if err := Forget(); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if status, err := GetStatus(); err != nil || status.Enabled {
		t.Fatalf("after Forget telemetry must be disabled (err=%v, enabled=%v)", err, status.Enabled)
	}
}
