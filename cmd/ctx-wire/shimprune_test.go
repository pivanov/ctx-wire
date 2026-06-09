package main

import "testing"

// TestShouldFlagRedundantShims is the truth table for the redundant-shims advisory
// (no deletion happens; this only decides whether to NUDGE). Load-bearing gates:
// uses == 0 (any recorded shim use means a steering agent relied on them -> don't
// nudge) and active > 0 (only flag shims actually on the hot path, not harmless
// shadowed ones). Counts are aggregated across every managed shim dir; the keep
// marker is handled by the caller, not this predicate.
func TestShouldFlagRedundantShims(t *testing.T) {
	cases := []struct {
		name        string
		installed   int
		active      int
		uses        int
		hookCovered bool
		steering    bool
		want        bool
	}{
		{"hook-only, unused, on hot path -> nudge (the common case)", 2, 2, 0, true, false, true},
		{"recorded use (steering relied on them) -> quiet", 2, 2, 7, true, false, false},
		{"installed but shadowed (active 0) -> quiet (no latency to fix)", 2, 0, 0, true, false, false},
		{"no hook/plugin coverage -> quiet (shims may be only coverage)", 2, 2, 0, false, false, false},
		{"steering agent configured here -> quiet", 2, 2, 0, true, true, false},
		{"nothing installed -> quiet", 0, 0, 0, true, false, false},
	}
	for _, tc := range cases {
		got := shouldFlagRedundantShims(tc.installed, tc.active, tc.uses, tc.hookCovered, tc.steering)
		if got != tc.want {
			t.Errorf("%s: shouldFlagRedundantShims = %v, want %v", tc.name, got, tc.want)
		}
	}
}
