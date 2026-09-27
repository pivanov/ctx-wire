package rewrite

import "testing"

// TestShellScriptArgs exercises ShellScriptArgs directly, against the argv form
// a shell invocation arrives in when the hook never sees inside it (e.g. `sh -lc
// '...'` typed by an agent, executed via exec rather than through a line the
// shell rewriter can see as text).
func TestShellScriptArgs(t *testing.T) {
	const wrap = "ctx-wire run "

	// lookPath is stubbed so these cases resolve deterministically regardless of
	// the host PATH; save and restore per the pattern in lookpath_test.go.
	prev := lookPath
	lookPath = func(name string) bool {
		switch name {
		case "git", "rg", "ls", "cat":
			return true
		default:
			return false
		}
	}
	t.Cleanup(func() { lookPath = prev })

	tests := []struct {
		name     string
		prog     string
		args     []string
		wantOK   bool
		wantArgs []string
	}{
		{
			name:     "sh -lc compound script wrapped, args[0] unchanged",
			prog:     "sh",
			args:     []string{"-lc", "git status && git diff --stat"},
			wantOK:   true,
			wantArgs: []string{"-lc", "ctx-wire run git status && ctx-wire run git diff --stat"},
		},
		{
			name:     "extra positional args after the script are preserved",
			prog:     "sh",
			args:     []string{"-c", "git status", "argv0", "x"},
			wantOK:   true,
			wantArgs: []string{"-c", "ctx-wire run git status", "argv0", "x"},
		},
		{
			name:     "separate flag before -c",
			prog:     "sh",
			args:     []string{"-e", "-c", "git status"},
			wantOK:   true,
			wantArgs: []string{"-e", "-c", "ctx-wire run git status"},
		},
		{
			name:     "combined flag before -c",
			prog:     "sh",
			args:     []string{"-ec", "git status"},
			wantOK:   true,
			wantArgs: []string{"-ec", "ctx-wire run git status"},
		},
		{
			name:     "option that takes an argument before -c",
			prog:     "bash",
			args:     []string{"-o", "pipefail", "-c", "git status"},
			wantOK:   true,
			wantArgs: []string{"-o", "pipefail", "-c", "ctx-wire run git status"},
		},
		{
			name:     "long option before -c, bash --norc -c style",
			prog:     "bash",
			args:     []string{"--norc", "-c", "git status"},
			wantOK:   true,
			wantArgs: []string{"--norc", "-c", "ctx-wire run git status"},
		},
		{
			name:   "not a shell program",
			prog:   "git",
			args:   []string{"-c", "x"},
			wantOK: false,
		},
		{
			name:   "no -c flag at all",
			prog:   "sh",
			args:   []string{"script.sh"},
			wantOK: false,
		},
		{
			name:   "-c with no script argument following it",
			prog:   "sh",
			args:   []string{"-c"},
			wantOK: false,
		},
		{
			name:   "-- before -c blocks the scan",
			prog:   "sh",
			args:   []string{"--", "-c", "git status"},
			wantOK: false,
		},
		{
			name:   "nothing wrappable inside the script, echo is a builtin",
			prog:   "sh",
			args:   []string{"-c", "echo hi"},
			wantOK: false,
		},
		{
			name:   "command substitution in the script bails",
			prog:   "sh",
			args:   []string{"-c", "git log $(git rev-parse HEAD)"},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			argsBefore := append([]string(nil), tt.args...)

			got, ok := ShellScriptArgs(tt.prog, tt.args, wrap)

			if ok != tt.wantOK {
				t.Fatalf("ShellScriptArgs(%q, %v) ok = %v, want %v", tt.prog, tt.args, ok, tt.wantOK)
			}
			if ok {
				if len(got) != len(tt.wantArgs) {
					t.Fatalf("ShellScriptArgs(%q, %v) = %v, want %v", tt.prog, tt.args, got, tt.wantArgs)
				}
				for i := range got {
					if got[i] != tt.wantArgs[i] {
						t.Errorf("ShellScriptArgs(%q, %v)[%d] = %q, want %q", tt.prog, tt.args, i, got[i], tt.wantArgs[i])
					}
				}
			}

			// The input slice must never be mutated, callers may reuse it.
			for i := range tt.args {
				if tt.args[i] != argsBefore[i] {
					t.Errorf("input args mutated at index %d: got %q, want %q", i, tt.args[i], argsBefore[i])
				}
			}
		})
	}
}
