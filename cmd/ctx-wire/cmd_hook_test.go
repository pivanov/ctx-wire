package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdio swaps os.Stdout/os.Stderr, calls fn, restores them, and returns
// the captured stdout and stderr. Readers start draining before fn runs so a
// large stream can never wedge the pipe.
func captureStdio(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr

	chOut := make(chan string)
	chErr := make(chan string)
	go func() { b, _ := io.ReadAll(rOut); chOut <- string(b) }()
	go func() { b, _ := io.ReadAll(rErr); chErr <- string(b) }()

	fn()

	// Close the write ends so the readers see EOF, then restore and return.
	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	return <-chOut, <-chErr
}

// TestCmdRewriteFlags exercises the --shell/--agent flag parsing and dispatches
// through deterministic commands only: alias/cmdlet/builtin passthrough needs no
// PATH lookup, so the assertions hold on any host (no git, no curl required).
func TestCmdRewriteFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		out  string // expected stdout ("" => must equal input line below)
	}{
		{
			// PowerShell recognizer: curl is a default alias (not wrapped, PATH-independent).
			name: "powershell alias passthrough",
			args: []string{"--shell", "powershell", "curl -s https://example.com"},
			code: 0,
			out:  "curl -s https://example.com",
		},
		{
			// cmdlets are not executables, so they never wrap (PATH-independent).
			name: "powershell cmdlet passthrough",
			args: []string{"--shell", "powershell", "Get-ChildItem D:/src"},
			code: 0,
			out:  "Get-ChildItem D:/src",
		},
		{
			// --shell= form and flags in either order still route to PowerShell.
			name: "shell equals form plus agent",
			args: []string{"--shell=powershell", "--agent", "pi", "where foo"},
			code: 0,
			out:  "where foo", // alias, stays unwrapped
		},
		{
			// flags in any leading order (--agent first).
			name: "agent before shell",
			args: []string{"--agent", "pi", "--shell", "powershell", "curl -s x"},
			code: 0,
			out:  "curl -s x",
		},
		{
			// Case-insensitive shell value still dispatches to PowerShell.
			name: "shell value case-insensitive",
			args: []string{"--shell=PowerShell", "curl -s x"},
			code: 0,
			out:  "curl -s x",
		},
		{
			// Default shell is bash; a bash builtin is never wrapped (PATH-independent).
			name: "bash default builtin passthrough",
			args: []string{"--agent", "pi", "cd D:/src"},
			code: 0,
			out:  "cd D:/src",
		},
		{
			// Explicit bash shell is the same recognizer as the default.
			name: "bash explicit",
			args: []string{"--shell", "bash", "cd D:/src"},
			code: 0,
			out:  "cd D:/src",
		},
		{
			name: "no args is usage error",
			args: []string{},
			code: 2,
		},
		{
			name: "shell flag missing value",
			args: []string{"--shell"},
			code: 2,
		},
		{
			name: "shell equals empty value",
			args: []string{"--shell=", "git status"},
			code: 2,
		},
		{
			name: "agent flag missing value",
			args: []string{"--agent"},
			code: 2,
		},
		{
			name: "agent equals empty value",
			args: []string{"--agent=", "git status"},
			code: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, _ := captureStdio(t, func() {
				if code := cmdRewrite(tt.args); code != tt.code {
					t.Fatalf("cmdRewrite(%q) exit = %d, want %d", tt.args, code, tt.code)
				}
			})
			if tt.out != "" {
				if got := strings.TrimSpace(stdout); got != tt.out {
					t.Errorf("cmdRewrite(%q) = %q, want %q", tt.args, got, tt.out)
				}
			}
		})
	}
}
