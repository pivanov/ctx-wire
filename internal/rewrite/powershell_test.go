package rewrite

import (
	"strings"
	"testing"
)

// stubPowerShellPath makes every name in resolvable look like an executable on
// PATH, and nothing else. Real Windows PATH lookups are not available in a Unix
// test run, and the point of these tests is the DECISION, not the lookup.
func stubPowerShellPath(t *testing.T, resolvable ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, n := range resolvable {
		set[n] = true
	}
	prev := lookPath
	lookPath = func(name string) bool { return set[name] }
	t.Cleanup(func() { lookPath = prev })
}

func TestPowerShellWrapsExternalPrograms(t *testing.T) {
	stubPowerShellPath(t, "git", "go", "npm", "docker", "rg", "docker-compose")
	cases := []struct {
		line string
		want string
	}{
		{"git status", "ctx-wire run --agent copilot git status"},
		{"go test ./...", "ctx-wire run --agent copilot go test ./..."},
		{"npm run build", "ctx-wire run --agent copilot npm run build"},
		{"docker ps", "ctx-wire run --agent copilot docker ps"},
		{`rg TODO D:\src`, `ctx-wire run --agent copilot rg TODO D:\src`},
		// A hyphenated name is not automatically a cmdlet; docker-compose is a
		// real executable and resolves, so it must still be wrapped.
		{"docker-compose up", "ctx-wire run --agent copilot docker-compose up"},
		// Surrounding whitespace must not defeat the match.
		{"  git status  ", "ctx-wire run --agent copilot git status"},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			if got := PowerShellLineForAgent(c.line, "copilot"); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// Cmdlets and PowerShell functions are not on PATH. Wrapping one would exec a
// program that does not exist and turn a working command into "command not
// found", which is the failure mode ctx-wire must never cause.
func TestPowerShellLeavesCmdletsAlone(t *testing.T) {
	stubPowerShellPath(t, "git")
	for _, line := range []string{
		`Get-ChildItem "D:\Visual Studio\edydox"`,
		"Select-Object Name",
		"Write-Host hello",
		"myCustomFunction arg",
	} {
		t.Run(line, func(t *testing.T) {
			if got := PowerShellLineForAgent(line, "copilot"); got != line {
				t.Errorf("cmdlet was rewritten to %q; it is not an executable", got)
			}
		})
	}
}

// The dangerous case: these names resolve to a real .exe AND mean something
// different in PowerShell. `curl` is Invoke-WebRequest, `where` is Where-Object,
// `sort` is Sort-Object. Wrapping them runs the executable with cmdlet
// arguments, which is worse than not wrapping at all.
func TestPowerShellLeavesShadowingAliasesAlone(t *testing.T) {
	shadowing := []string{"curl", "where", "sort", "ls", "cat", "tee", "diff", "ps", "sleep", "kill"}
	stubPowerShellPath(t, shadowing...) // all resolvable, exactly the trap
	for _, name := range shadowing {
		t.Run(name, func(t *testing.T) {
			line := name + " something"
			if got := PowerShellLineForAgent(line, "copilot"); got != line {
				t.Errorf("%s is a PowerShell alias for a cmdlet but was wrapped: %q", name, got)
			}
		})
	}
}

// Anything with PowerShell syntax this recognizer does not model passes through.
func TestPowerShellLeavesOperatorsAlone(t *testing.T) {
	stubPowerShellPath(t, "git", "go", "npm", "rg")
	for _, line := range []string{
		"git log | Select-Object -First 5", // object pipeline
		"git status; git diff",             // statement separator
		"npm ci && npm test",               // chain
		"git status > out.txt",             // redirection
		"git log $branch",                  // variable expansion
		"git checkout $(git rev-parse HEAD)",
		"& git status",  // call operator
		"git diff @{1}", // splat / hashtable
	} {
		t.Run(line, func(t *testing.T) {
			if got := PowerShellLineForAgent(line, "copilot"); got != line {
				t.Errorf("line with unmodelled syntax was rewritten to %q", got)
			}
		})
	}
}

// A command the model already typed as `ctx-wire run ...` is wrapped but
// unattributed. Stamp the agent so the savings are not recorded as
// (unattributed), matching the bash path.
func TestPowerShellStampsAlreadyWrappedCommands(t *testing.T) {
	stubPowerShellPath(t, "git")
	got := PowerShellLineForAgent("ctx-wire run git status", "copilot")
	want := "ctx-wire run --agent copilot git status"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// Without an agent the plain prefix is used, and a wrapped line stays wrapped
// rather than being wrapped twice.
func TestPowerShellIsIdempotent(t *testing.T) {
	stubPowerShellPath(t, "git")
	once := PowerShellLineForAgent("git status", "copilot")
	twice := PowerShellLineForAgent(once, "copilot")
	if twice != once {
		t.Errorf("second pass changed the line:\nfirst  %q\nsecond %q", once, twice)
	}
	if strings.Count(twice, "ctx-wire run") != 1 {
		t.Errorf("command was wrapped more than once: %q", twice)
	}
}

// Shape rules shared with the POSIX path still apply, so the two shells cannot
// drift on what counts as unwrappable.
func TestPowerShellHonorsSharedShapeRules(t *testing.T) {
	stubPowerShellPath(t, "cd", "git", "vim")
	for _, line := range []string{
		"cd D:/src", // shell builtin
		"",          // empty
	} {
		if got := PowerShellLineForAgent(line, "copilot"); got != line {
			t.Errorf("%q was rewritten to %q", line, got)
		}
	}
}
