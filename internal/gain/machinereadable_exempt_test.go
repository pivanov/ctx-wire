package gain

import "testing"

// Machine-readable passthrough samples (git --porcelain/-z/--format) intentionally
// bypass filtering, so explain/tune must treat them as hook-limited, not advise
// "add a built-in filter" for output that must never be filtered.
func TestIsHookLimitedSampleMachineReadable(t *testing.T) {
	cases := []struct {
		sample string
		want   bool
	}{
		{"git status --porcelain", true},
		{"git status -z", true},
		{"git log --format=%H%x00%s", true},
		{"ctx-wire run git status --porcelain -z", true}, // run-prefixed sample
		{"git status", false},                            // human format stays advisable
	}
	for _, c := range cases {
		if got := isHookLimitedSample("git", c.sample); got != c.want {
			t.Errorf("isHookLimitedSample(git, %q) = %v, want %v", c.sample, got, c.want)
		}
	}
}
