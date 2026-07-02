package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDue(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		last     time.Time
		interval time.Duration
		want     bool
	}{
		{"never checked", time.Time{}, 6 * time.Hour, true},
		{"just checked", now.Add(-1 * time.Hour), 6 * time.Hour, false},
		{"exactly at interval", now.Add(-6 * time.Hour), 6 * time.Hour, true},
		{"past interval", now.Add(-7 * time.Hour), 6 * time.Hour, true},
		{"zero interval uses default", now.Add(-1 * time.Hour), 0, false},
		{"zero interval past default", now.Add(-7 * time.Hour), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := due(tt.last, now, tt.interval); got != tt.want {
				t.Fatalf("due(%v, now, %v) = %v, want %v", tt.last, tt.interval, got, tt.want)
			}
		})
	}
}

func TestLastCheckRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if got := readLastCheck(); !got.IsZero() {
		t.Fatalf("readLastCheck with no file = %v, want zero", got)
	}

	want := time.Date(2026, 6, 6, 9, 30, 0, 0, time.UTC)
	if err := writeLastCheck(want); err != nil {
		t.Fatalf("writeLastCheck: %v", err)
	}
	got := readLastCheck()
	if !got.Equal(want) {
		t.Fatalf("readLastCheck = %v, want %v", got, want)
	}

	p, err := statePath()
	if err != nil {
		t.Fatalf("statePath: %v", err)
	}
	if base := filepath.Base(p); base != "update.json" {
		t.Fatalf("state file = %q, want update.json", base)
	}
}

func TestShouldCheckOnCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"run", true},
		{"hook", true},
		{"mcp", true},
		{"gain", true},
		{"doctor", true},
		{"", false},
		{"update", false},
		{"uninstall", false},
		{autoUpdateArg, false},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := ShouldCheckOnCommand(tt.cmd); got != tt.want {
				t.Fatalf("ShouldCheckOnCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestMaybeBackgroundUpdateThrottlesAndSkipsDev(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	// A dev/unversioned build must never stamp a check or spawn.
	MaybeBackgroundUpdate("dev", 6*time.Hour)
	if !readLastCheck().IsZero() {
		t.Fatal("dev build should not record a check")
	}

	// Disabled via env: also a no-op.
	t.Setenv(EnvDisable, "1")
	MaybeBackgroundUpdate("0.1.0", 6*time.Hour)
	if !readLastCheck().IsZero() {
		t.Fatal("CTX_WIRE_NO_AUTOUPDATE should suppress the check")
	}

	// A recent check throttles: a stamp in the future window means not due, so
	// MaybeBackgroundUpdate returns without overwriting it.
	t.Setenv(EnvDisable, "")
	stamp := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	if err := writeLastCheck(stamp); err != nil {
		t.Fatalf("writeLastCheck: %v", err)
	}
	MaybeBackgroundUpdate("0.1.0", 6*time.Hour)
	if got := readLastCheck(); !got.Equal(stamp) {
		t.Fatalf("throttled call rewrote stamp: got %v, want %v", got, stamp)
	}
}

func TestMaybeBackgroundUpdateStampsBeforeSpawning(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	oldNow := nowFunc
	nowFunc = func() time.Time { return now }
	oldSpawn := spawnDetachedFunc
	spawned := 0
	spawnDetachedFunc = func() { spawned++ }
	t.Cleanup(func() {
		nowFunc = oldNow
		spawnDetachedFunc = oldSpawn
	})

	MaybeBackgroundUpdate("0.1.0", time.Hour)
	if spawned != 1 {
		t.Fatalf("spawned = %d, want 1", spawned)
	}
	if got := readLastCheck(); !got.Equal(now) {
		t.Fatalf("last check = %v, want %v", got, now)
	}
	lock, err := claimLockPath()
	if err != nil {
		t.Fatalf("claimLockPath: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("claim lock should be removed after scheduling, stat err = %v", err)
	}

	MaybeBackgroundUpdate("0.1.0", time.Hour)
	if spawned != 1 {
		t.Fatalf("throttled call spawned again: %d", spawned)
	}
}

func TestMaybeBackgroundUpdateClaimLockSuppressesDuplicateAndRecoversStale(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	oldNow := nowFunc
	nowFunc = func() time.Time { return now }
	oldSpawn := spawnDetachedFunc
	spawned := 0
	spawnDetachedFunc = func() { spawned++ }
	t.Cleanup(func() {
		nowFunc = oldNow
		spawnDetachedFunc = oldSpawn
	})

	lock, err := claimLockPath()
	if err != nil {
		t.Fatalf("claimLockPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(lock, []byte("busy\n"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := os.Chtimes(lock, now, now); err != nil {
		t.Fatalf("chtimes fresh lock: %v", err)
	}

	MaybeBackgroundUpdate("0.1.0", time.Hour)
	if spawned != 0 {
		t.Fatalf("fresh lock should suppress spawn, spawned = %d", spawned)
	}
	if got := readLastCheck(); !got.IsZero() {
		t.Fatalf("fresh lock should not stamp last check, got %v", got)
	}

	stale := now.Add(-2 * claimLockMaxAge)
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatalf("chtimes stale lock: %v", err)
	}
	MaybeBackgroundUpdate("0.1.0", time.Hour)
	if spawned != 1 {
		t.Fatalf("stale lock should recover and spawn once, spawned = %d", spawned)
	}
	if got := readLastCheck(); !got.Equal(now) {
		t.Fatalf("last check after stale recovery = %v, want %v", got, now)
	}
}
