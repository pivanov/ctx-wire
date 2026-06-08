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

		// find/fd/sort are read-only only without their side-effecting flags.
		{"find", []string{".", "-name", "*.go"}, true},
		{"find", []string{".", "-delete"}, false},
		{"find", []string{".", "-exec", "rm", "{}", ";"}, false},
		{"find", []string{".", "-fprintf", "out.txt", "%p"}, false},
		{"fd", []string{"-e", "go"}, true},
		{"fd", []string{"foo", "-x", "rm"}, false},
		{"fd", []string{"foo", "--exec-batch", "rm"}, false},
		{"sort", []string{"file"}, true},
		{"sort", []string{"-k2", "file"}, true},
		{"sort", []string{"-o", "out", "file"}, false},
		{"sort", []string{"-oout", "file"}, false},
		{"sort", []string{"--output=out", "file"}, false},

		// env can exec an arbitrary command, so it is never dedup-eligible.
		{"env", nil, false},
		{"env", []string{"FOO=bar", "rm", "-rf", "x"}, false},
	}
	for _, c := range cases {
		if got := IsDedupEligible(c.name, c.args); got != c.want {
			t.Errorf("IsDedupEligible(%q, %v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}
