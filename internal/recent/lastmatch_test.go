package recent

import (
	"testing"
	"time"
)

func TestLastMatch(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	now := time.Now()
	Record(Options{Enabled: true}, Entry{
		TS: now.UTC().Format(time.RFC3339Nano), Command: "git status", Emitted: "clean",
	})

	// A recent matching command matches within the window.
	if e, ok := LastMatch("git status", time.Hour, now); !ok || e.Command != "git status" {
		t.Errorf("expected a match for the recent entry, got ok=%v", ok)
	}
	// The stored hash is set, so a caller can compare.
	if e, _ := LastMatch("git status", time.Hour, now); e.Hash == "" {
		t.Error("matched entry should carry a hash")
	}
	// A different command does not match.
	if _, ok := LastMatch("ls", time.Hour, now); ok {
		t.Error("a different command must not match")
	}
	// Outside the recency window, no match.
	if _, ok := LastMatch("git status", time.Minute, now.Add(2*time.Hour)); ok {
		t.Error("a match older than the window must be rejected")
	}
}
