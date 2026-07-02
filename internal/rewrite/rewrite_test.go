package rewrite

import "testing"

func TestLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple command wrapped", "git status", "ctx-wire run git status"},
		{"command with args", "dotnet build -c Release", "ctx-wire run dotnet build -c Release"},
		{"compound && both wrapped", "git add . && git commit -m x",
			"ctx-wire run git add . && ctx-wire run git commit -m x"},
		{"single-quoted arg ending in backslash still splits at &&", `git commit -m 'x\' && rm -rf /tmp/y`,
			`ctx-wire run git commit -m 'x\' && ctx-wire run rm -rf /tmp/y`},
		{"single-quoted arg ending in backslash still splits pipeline", `cat 'a\' | wc -l`,
			`cat 'a\' | ctx-wire run wc -l`},
		{"single-quoted arg ending in backslash still detects redirect", `grep 'foo\' > /tmp/out`,
			`grep 'foo\' > /tmp/out`},
		{"compound ; wrapped", "make; make test", "ctx-wire run make; ctx-wire run make test"},
		{"|| wraps command after shell test", "test -f f || touch f", "test -f f || ctx-wire run touch f"},
		{"pipeline wraps last stage", "cat log | grep err", "cat log | ctx-wire run grep err"},
		{"three-stage pipeline wraps only last stage", "cat log | grep err | sort",
			"cat log | grep err | ctx-wire run sort"},
		{"background pipeline wraps last stage, keeps &", "cat log | grep err &",
			"cat log | ctx-wire run grep err &"},
		{"pipeline unwrappable last stage untouched", "cat a | grep b > out.txt", "cat a | grep b > out.txt"},
		{"mixed: compound wrapped, pipeline last stage wrapped",
			"npm run build && cat out | tail -5",
			"ctx-wire run npm run build && cat out | ctx-wire run tail -5"},
		{"for loop stays unchanged", `for f in a b; do echo "$f"; done`, `for f in a b; do echo "$f"; done`},
		{"if block stays unchanged", "if test -f README.md; then cat README.md; fi", "if test -f README.md; then cat README.md; fi"},
		{"while loop stays unchanged", "while read l; do echo x; done", "while read l; do echo x; done"},
		{"builtin cd not wrapped", "cd /tmp", "cd /tmp"},
		{"builtin cd then command", "cd /tmp && ls -la",
			"cd /tmp && ctx-wire run ls -la"},
		{"ls and git status both wrapped", "ls && git status",
			"ctx-wire run ls && ctx-wire run git status"},
		{"time prefix rewrites timed command", "time go test ./...",
			"time ctx-wire run go test ./..."},
		{"time -p prefix rewrites timed command", "time -p go build",
			"time -p ctx-wire run go build"},
		{"bare time stays unchanged", "time", "time"},
		{"time of a builtin stays unchanged", "time cd /tmp", "time cd /tmp"},
		{"timeout is not the time keyword", "timeout 5 go test",
			"ctx-wire run timeout 5 go test"},
		{"bash -lc rewrites inner command", "bash -lc 'bun lint'", "bash -lc 'ctx-wire run bun lint'"},
		{"bash -lc rewrites compound inner command", "bash -lc 'bun lint && git status'",
			"bash -lc 'ctx-wire run bun lint && ctx-wire run git status'"},
		{"zsh -l -c rewrites inner command", "zsh -l -c 'git status'", "zsh -l -c 'ctx-wire run git status'"},
		{"bash long option before -c rewrites inner command", "bash --norc -c 'git status'", "bash --norc -c 'ctx-wire run git status'"},
		{"bash option with argument before -c rewrites inner command", "bash -o pipefail -c 'git status'", "bash -o pipefail -c 'ctx-wire run git status'"},
		{"bash -lc shell builtin inner passthrough", "bash -lc 'echo ok'", "bash -lc 'echo ok'"},
		{"dynamic shell command string passthrough", `bash -lc "$cmd"`, `bash -lc "$cmd"`},
		{"command substitution shell command string passthrough", `bash -lc "$(echo git status)"`, `bash -lc "$(echo git status)"`},
		{"dynamic command token passthrough", `$cmd`, `$cmd`},
		{"command substitution command token passthrough", `$(which git) status`, `$(which git) status`},
		{"bracket test passthrough", "[ -n '' ]", "[ -n '' ]"},
		{"already wrapped left alone", "ctx-wire run git status", "ctx-wire run git status"},
		{"ctx-wire command left alone", "ctx-wire gain", "ctx-wire gain"},
		{"local ctx-wire command left alone", "./ctx-wire gain", "./ctx-wire gain"},
		{"env assignment prefix peels and wraps inner", "FOO=bar deploy", "FOO=bar ctx-wire run deploy"},
		{"quoted multi assignment preserved", `FOO="a b" BAR=x git status`, `FOO="a b" BAR=x ctx-wire run git status`},
		{"assignment only passthrough", "FOO=bar", "FOO=bar"},
		{"env with assignments wraps inner", "env FOO=bar git status", "env FOO=bar ctx-wire run git status"},
		{"env with flags passthrough", "env -i FOO=bar git status", "env -i FOO=bar git status"},
		{"command prefix wraps inner", "command git status", "command ctx-wire run git status"},
		{"command -v lookup passthrough", "command -v git", "command -v git"},
		{"command -V lookup passthrough", "command -V git", "command -V git"},
		{"exec stays passthrough", "exec node server.js", "exec node server.js"},
		{"subshell passthrough", "(git status)", "(git status)"},
		{"brace group passthrough", "{ git status; }", "{ git status; }"},
		{"quoted operator not split", `git commit -m "a && b"`, `ctx-wire run git commit -m "a && b"`},
		{"quoted pipe in builtin passthrough", `echo "a | b"`, `echo "a | b"`},
		// Redirections pass through untouched, like pipelines.
		{"redirect > passthrough", "cat source > dest", "cat source > dest"},
		{"redirect >> passthrough", "echo x >> file", "echo x >> file"},
		{"redirect 2> passthrough", "cmd 2> err.log", "cmd 2> err.log"},
		{"redirect stderr-to-stdout passthrough", "make 2>&1", "make 2>&1"},
		{"redirect < passthrough", "cmd < input", "cmd < input"},
		{"quoted > in builtin passthrough", `echo "a > b"`, `echo "a > b"`},
		{"redirect segment skipped, plain wrapped", "make > log && ls",
			"make > log && ctx-wire run ls"},
		{"empty stays empty", "", ""},
		{"whitespace preserved around operator", "make  &&  ls",
			"ctx-wire run make  &&  ctx-wire run ls"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := Line(tt.in); got != tt.want {
				t.Errorf("Line(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLineForAgent(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		in    string
		want  string
	}{
		{"simple command", "claude", "git status", "ctx-wire run --agent claude git status"},
		{"compound", "codex", "git status && ls", "ctx-wire run --agent codex git status && ctx-wire run --agent codex ls"},
		{"time prefix", "gemini", "time go test", "time ctx-wire run --agent gemini go test"},
		{"command prefix", "opencode", "command git status", "command ctx-wire run --agent opencode git status"},
		{"already marked", "claude", "ctx-wire run --agent claude git status", "ctx-wire run --agent claude git status"},
		{"invalid agent falls back", "bad value", "git status", "ctx-wire run git status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LineForAgent(tt.in, tt.agent); got != tt.want {
				t.Fatalf("LineForAgent(%q, %q) = %q, want %q", tt.in, tt.agent, got, tt.want)
			}
		})
	}
}

func TestLineIdempotent(t *testing.T) {
	once := Line("git status && make test")
	twice := Line(once)
	if once != twice {
		t.Errorf("rewrite not idempotent:\n once:  %q\n twice: %q", once, twice)
	}
}

// TestLineForAgentStampsWrappedRuns pins the attribution stamp: an explicitly
// typed `ctx-wire run <cmd>` (the form agent instructions teach) gains
// `--agent <name>` so sandboxed shells (where the ps-walk is blind) still
// attribute savings. Fail-open shapes are enumerated: explicit --agent wins,
// flag-first invocations and non-run subcommands stay untouched, and the
// no-agent rewrite (plain Line) never stamps.
func TestLineForAgentStampsWrappedRuns(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		in    string
		want  string
	}{
		{"bare wrapped run gains the stamp", "claude",
			"ctx-wire run sed -n '1,10p' f.go",
			"ctx-wire run --agent claude sed -n '1,10p' f.go"},
		{"explicit agent wins, no double stamp", "claude",
			"ctx-wire run --agent codex git status",
			"ctx-wire run --agent codex git status"},
		{"flag-first invocation left alone", "claude",
			"ctx-wire run --shim git status",
			"ctx-wire run --shim git status"},
		{"non-run subcommand left alone", "claude",
			"ctx-wire gain",
			"ctx-wire gain"},
		{"path-qualified binary keeps its path", "claude",
			"/usr/local/bin/ctx-wire run git status",
			"/usr/local/bin/ctx-wire run --agent claude git status"},
		// The stub lookPath makes every binary wrappable, so the assertion for
		// a non-ctx-wire basename is: normally WRAPPED (stamp did not fire and
		// inject --agent into a foreign binary's argv).
		{"non-ctx-wire binary wraps normally, never stamped", "claude",
			"/definitely/missing/cw-dev run git status",
			"ctx-wire run --agent claude /definitely/missing/cw-dev run git status"},
		{"pipeline tail gets stamped", "claude",
			"rg TODO . | ctx-wire run wc -l",
			"rg TODO . | ctx-wire run --agent claude wc -l"},
		{"env prefix peeled then stamped", "claude",
			"FOO=bar ctx-wire run git status",
			"FOO=bar ctx-wire run --agent claude git status"},
	}
	for _, tt := range tests {
		if got := LineForAgent(tt.in, tt.agent); got != tt.want {
			t.Errorf("%s: LineForAgent(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
	// Plain Line (no agent) must never stamp: there is nothing to attribute.
	if got := Line("ctx-wire run git status"); got != "ctx-wire run git status" {
		t.Errorf("Line must not stamp without an agent, got %q", got)
	}
}
