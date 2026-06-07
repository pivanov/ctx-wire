package runner

import (
	"strings"
	"testing"

	"ctx-wire/internal/recent"
)

// TestRecordRecentScrubsRaw is the security test for retention: the raw
// (pre-filter) body the runner stores must be scrubbed before it reaches disk,
// so a secret in raw output never lands in the recent-outputs store.
func TestRecordRecentScrubsRaw(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	SetRetention(recent.Options{Enabled: true, RawBodies: true})
	defer SetRetention(recent.Options{})

	const secret = "ghp_0123456789012345678901234567890123456"
	outCap := &capWriter{max: maxCapture}
	_, _ = outCap.Write([]byte("token: " + secret + "\nclean line\n"))
	errCap := &capWriter{max: maxCapture}

	// emitted is already scrubbed in production; the raw is not, so recordRecent
	// must scrub it.
	recordRecent("gh auth status", "gh", "filtered", outCap, errCap, "clean line\n", "", 0)

	entries := recent.List()
	if len(entries) != 1 {
		t.Fatalf("got %d retained entries, want 1", len(entries))
	}
	if strings.Contains(entries[0].Raw, "ghp_") {
		t.Errorf("the raw body must be scrubbed before disk; secret leaked: %q", entries[0].Raw)
	}
	if !strings.Contains(entries[0].Raw, "clean line") {
		t.Errorf("the benign raw content should survive scrubbing: %q", entries[0].Raw)
	}
}

// TestRecordRecentDisabled confirms nothing is stored when retention is off.
func TestRecordRecentDisabled(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	SetRetention(recent.Options{})
	outCap := &capWriter{max: maxCapture}
	_, _ = outCap.Write([]byte("hello\n"))
	recordRecent("echo hello", "", "passthrough", outCap, &capWriter{max: maxCapture}, "hello\n", "", 0)
	if got := recent.List(); len(got) != 0 {
		t.Errorf("retention off should store nothing, got %d", len(got))
	}
}
