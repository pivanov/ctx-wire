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
	// The crucial guarantee: telemetry stays DISABLED after withdrawal. Forget
	// persists an explicit disabled consent (not a deleted config), so withdrawal
	// is recorded as a deliberate choice and sticks.
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

// TestForgetUnderOptOut pins the opt-out model: with no env override telemetry is
// ON by default and the one-time notice shows for an undecided (nil) user; Forget
// records an explicit withdrawal ({enabled:false}), so telemetry goes off and
// stays off and the notice must NOT re-appear. Only nil is ever migrated to on,
// so this recorded "false" is what a later update must never reverse.
func TestForgetUnderOptOut(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "telemetry-state.json"))
	t.Setenv(envEnabled, "") // no override: exercise the opt-out default

	if status, err := GetStatus(); err != nil || !status.Enabled {
		t.Fatalf("precondition: telemetry must be ON by default under opt-out (err=%v, enabled=%v)", err, status.Enabled)
	}
	if !ShouldPreviewConsent() {
		t.Fatal("an undecided user should see the one-time telemetry notice")
	}
	if err := Forget(); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if status, err := GetStatus(); err != nil || status.Enabled {
		t.Fatalf("after Forget telemetry must be disabled and stay disabled (err=%v, enabled=%v)", err, status.Enabled)
	}
	if ShouldPreviewConsent() {
		t.Fatal("after Forget (an explicit withdrawal) the notice must not re-appear")
	}
}
