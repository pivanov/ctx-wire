package main

import "testing"

func TestRemovedLines(t *testing.T) {
	// b and d are in raw but not emitted.
	if got := removedLines("a\nb\nc\nd", "a\nc"); got != 2 {
		t.Errorf("removedLines = %d, want 2", got)
	}
	// Identical inputs remove nothing.
	if got := removedLines("a\nb", "a\nb"); got != 0 {
		t.Errorf("identical removed %d, want 0", got)
	}
	// A duplicate line removed once, not twice.
	if got := removedLines("x\nx\ny", "x\ny"); got != 1 {
		t.Errorf("multiset diff = %d, want 1", got)
	}
}
