package commandpolicy

import "testing"

func TestIsMachineReadable(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want bool
	}{
		{"git status porcelain", "git", []string{"status", "--porcelain"}, true},
		{"git status porcelain v2", "git", []string{"status", "--porcelain=v2"}, true},
		{"git status -z", "git", []string{"status", "-z"}, true},
		{"git -C path status porcelain -z", "/usr/bin/git", []string{"-C", "/repo", "status", "--porcelain", "-z"}, true},
		{"git diff name-only -z", "git", []string{"diff", "--name-only", "-z"}, true},
		{"git log --format template", "git", []string{"log", "--format=%H%x00%s"}, true},
		{"git log --pretty=format", "git", []string{"log", "--pretty=format:%h %s"}, true},
		{"git for-each-ref --format", "git", []string{"for-each-ref", "--format", "%(refname)"}, true},
		{"non-git --format ignored", "docker", []string{"ps", "--format", "{{.ID}}"}, false},
		{"human git status not machine", "git", []string{"status"}, false},
		{"human git status -s not machine", "git", []string{"status", "-s"}, false},
		{"tar -z is gzip not NUL", "tar", []string{"-z", "-c", "-f", "a.tgz", "."}, false},
		{"non-git -z ignored", "gzip", []string{"-z", "file"}, false},
		// Scope is git-only + --porcelain: a coincidental -0/--null (python/kill)
		// or generic NUL idiom is a separate per-tool follow-up, not covered here.
		{"grep --null not covered", "grep", []string{"--null", "-r", "needle"}, false},
		{"xargs -0 not covered", "xargs", []string{"-0", "rm"}, false},
		{"python -0 coincidental arg not machine", "python3", []string{"script.py", "-0"}, false},
		{"kill -0 not machine", "kill", []string{"-0", "1234"}, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMachineReadable(tt.cmd, tt.args); got != tt.want {
				t.Errorf("IsMachineReadable(%q, %v) = %v, want %v", tt.cmd, tt.args, got, tt.want)
			}
		})
	}
}
