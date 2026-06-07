package commandpolicy

import "testing"

func TestIsDedupEligible(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"cat", []string{"f"}, true},
		{"/bin/ls", []string{"-la"}, true},
		{"grep", []string{"x", "f"}, true},
		{"git", []string{"status"}, true},
		{"git", []string{"log", "--oneline"}, true},
		{"git", []string{"commit", "-m", "x"}, false}, // not read-only
		{"git", []string{"-C", "d", "status"}, false}, // flag-prefixed -> conservative
		{"git", nil, false},
		{"rm", []string{"-rf", "x"}, false},
		{"npm", []string{"install"}, false},
		{"sh", []string{"-c", "echo hi"}, false},
	}
	for _, c := range cases {
		if got := IsDedupEligible(c.name, c.args); got != c.want {
			t.Errorf("IsDedupEligible(%q, %v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}
