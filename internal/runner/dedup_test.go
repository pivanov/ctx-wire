package runner

import (
	"strings"
	"testing"
	"time"

	"ctx-wire/internal/recent"
)

func TestMaybeDedup(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	SetDedup(DedupOptions{Enabled: true, Recency: time.Hour})
	SetRetention(recent.Options{Enabled: true})
	defer func() { SetDedup(DedupOptions{}); SetRetention(recent.Options{}) }()

	// A prior run of `git status` with the same output, recorded just now.
	recent.Record(recent.Options{Enabled: true}, recent.Entry{
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
		Command: "git status",
		Emitted: "clean\n",
	})

	// Identical recent output -> dedup hit with a recoverable reference.
	ref, ok := maybeDedup("git", []string{"status"}, "git status", "clean\n", 0)
	if !ok {
		t.Fatal("expected a dedup hit for identical recent output")
	}
	if !strings.Contains(ref, "unchanged since") || !strings.Contains(ref, "inspect") {
		t.Errorf("reference missing expected recoverability text: %q", ref)
	}

	// Changed output must not dedup.
	if _, ok := maybeDedup("git", []string{"status"}, "git status", "dirty\n", 0); ok {
		t.Error("changed output must not dedup")
	}
	// An ineligible (non-read) command must not dedup even if output matches.
	if _, ok := maybeDedup("npm", []string{"test"}, "npm test", "clean\n", 0); ok {
		t.Error("ineligible command must not dedup")
	}
	// A failed command must always be shown.
	if _, ok := maybeDedup("git", []string{"status"}, "git status", "clean\n", 1); ok {
		t.Error("failed command (exit != 0) must not dedup")
	}
	// The escape hatch disables it.
	t.Setenv("CTX_WIRE_NO_DEDUP", "1")
	if _, ok := maybeDedup("git", []string{"status"}, "git status", "clean\n", 0); ok {
		t.Error("CTX_WIRE_NO_DEDUP must disable dedup")
	}
}

func TestMaybeDedupDisabled(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	SetDedup(DedupOptions{}) // off
	recent.Record(recent.Options{Enabled: true}, recent.Entry{
		TS: time.Now().UTC().Format(time.RFC3339Nano), Command: "ls", Emitted: "a\n",
	})
	if _, ok := maybeDedup("ls", nil, "ls", "a\n", 0); ok {
		t.Error("dedup off must never dedup")
	}
}

// TestMaybeDedupRetentionOff pins the kill-switch fix: with dedup enabled but the
// store disabled (e.g. CTX_WIRE_RETENTION=0), dedup must not fire even if a
// matching entry still sits on disk.
func TestMaybeDedupRetentionOff(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	SetDedup(DedupOptions{Enabled: true, Recency: time.Hour})
	defer SetDedup(DedupOptions{})
	recent.Record(recent.Options{Enabled: true}, recent.Entry{
		TS: time.Now().UTC().Format(time.RFC3339Nano), Command: "ls", Emitted: "a\n",
	})
	SetRetention(recent.Options{}) // store disabled
	defer SetRetention(recent.Options{})
	if _, ok := maybeDedup("ls", nil, "ls", "a\n", 0); ok {
		t.Error("retention disabled (kill switch) must disable dedup even with a match on disk")
	}
}
