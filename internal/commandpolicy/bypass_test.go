package commandpolicy

import "testing"

func TestIsMCPServer(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want bool
	}{
		// The reported case: trace_agent launched via bun, MCP token in the path.
		{"bun mcp path", "bun", []string{"/Users/x/edynamix/debug-agent/packages/mcp/src/bin.ts"}, true},
		{"bun absolute bun mcp path", "/Users/x/.bun/bin/bun", []string{"/srv/mcp/server.ts"}, true},
		{"npx modelcontextprotocol", "npx", []string{"-y", "@modelcontextprotocol/server-filesystem", "/data"}, true},
		{"bunx dash mcp", "bunx", []string{"some-mcp"}, true},
		{"uvx mcp-server", "uvx", []string{"mcp-server-fetch"}, true},
		{"deno jsr mcp", "deno", []string{"run", "-A", "jsr:@foo/mcp"}, true},
		{"compiled mcp binary, no launcher", "mcp-server-git", nil, true},
		{"compiled binary suffix mcp", "/opt/bin/github-mcp-server", []string{"stdio"}, true},
		{"ctx-wire mcp subcommand arg", "node", []string{"./fetch_mcp.js"}, true},

		// Must NOT be treated as MCP: real commands that merely mention mcp, or
		// launchers running ordinary scripts.
		{"go test in mcp dir", "go", []string{"test", "./mcp/..."}, false},
		{"grep for mcp", "grep", []string{"mcp", "file.txt"}, false},
		{"bun build script", "bun", []string{"run", "build"}, false},
		{"bun eval", "bun", []string{"-e", "console.log(1)"}, false},
		{"node ordinary script", "node", []string{"scripts/seed.js"}, false},
		{"npx ordinary tool", "npx", []string{"prettier", "--write", "."}, false},
		{"mcprc is not a token", "bun", []string{"mcprc.ts"}, false},
		{"adjacent mcp without delimiter is not a token", "bun", []string{"xmcpmcp"}, false},
		{"word-joined mcp is not a token", "bun", []string{"compmcpute"}, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMCPServer(tt.cmd, tt.args); got != tt.want {
				t.Fatalf("IsMCPServer(%q, %v) = %v, want %v", tt.cmd, tt.args, got, tt.want)
			}
		})
	}
}

func TestClassifyBypassMCP(t *testing.T) {
	bypass, reason := ClassifyBypass("bun", []string{"/srv/packages/mcp/src/bin.ts"})
	if !bypass {
		t.Fatalf("expected MCP launch to bypass capture")
	}
	if reason != "mcp stdio server" {
		t.Errorf("reason = %q, want %q", reason, "mcp stdio server")
	}

	// A normal build must still be captured/filtered.
	if bypass, _ := ClassifyBypass("bun", []string{"run", "build"}); bypass {
		t.Errorf("bun run build should not bypass capture")
	}
}

func TestClassifyBypassStreamingScoped(t *testing.T) {
	tests := []struct {
		name       string
		cmd        string
		args       []string
		wantBypass bool
	}{
		// MUST bypass (real streaming -- capturing these would hang):
		{"tail -f", "tail", []string{"-f", "/var/log/x"}, true},
		{"tail -F", "tail", []string{"-F", "/var/log/x"}, true},
		{"kubectl logs -f", "kubectl", []string{"logs", "-f", "pod"}, true},
		{"kubectl get -w", "kubectl", []string{"get", "pods", "-w"}, true},
		{"docker logs -f", "docker", []string{"logs", "-f", "ctr"}, true},
		{"journalctl -f", "journalctl", []string{"-u", "x", "-f"}, true},
		// MUST NOT bypass (the fix -- these must be captured + filtered + scrubbed):
		{"grep -F literal", "grep", []string{"-F", "literal", "file"}, false},
		{"grep -f patterns", "grep", []string{"-f", "patterns.txt", "file"}, false},
		{"ls -F", "ls", []string{"-F"}, false},
		{"sort -f", "sort", []string{"-f", "file"}, false},
		{"git log --follow", "git", []string{"log", "--follow", "x"}, false},
		{"git commit -F", "git", []string{"commit", "-F", "msg.txt"}, false},
		{"pnpm -F app test", "pnpm", []string{"-F", "app", "test"}, false},
		{"yarn -w build", "yarn", []string{"-w", "build"}, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, _ := ClassifyBypass(tt.cmd, tt.args)
			if got != tt.wantBypass {
				t.Fatalf("ClassifyBypass(%q, %v) bypass = %v, want %v", tt.cmd, tt.args, got, tt.wantBypass)
			}
		})
	}
}

func TestClassifyBypassInterpreters(t *testing.T) {
	tests := []struct {
		name       string
		cmd        string
		args       []string
		wantBypass bool
	}{
		// MUST bypass: bare invocation (REPL) or explicitly interactive
		{"python bare", "python", nil, true},
		{"python3 bare", "python3", nil, true},
		{"node bare", "node", nil, true},
		{"ipython bare", "ipython", nil, true},
		{"irb bare", "irb", nil, true},
		{"python -i explicit interactive", "python", []string{"-i", "x.py"}, true},
		{"python -m http.server long-running", "python", []string{"-m", "http.server"}, true},
		{"python3 -m uvicorn long-running", "python3", []string{"-m", "uvicorn", "app:app"}, true},

		// MUST NOT bypass: finite one-shot invocations (capture+scrub)
		{"python -c inline code", "python", []string{"-c", "print(1)"}, false},
		{"node -e inline code", "node", []string{"-e", "console.log(1)"}, false},
		{"python script.py", "python", []string{"script.py"}, false},
		{"python script with trailing -i is the script's flag, still finite", "python", []string{"migrate.py", "-i"}, false},
		{"node scripts/seed.js", "node", []string{"scripts/seed.js"}, false},
		{"python -m pytest", "python", []string{"-m", "pytest"}, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, _ := ClassifyBypass(tt.cmd, tt.args)
			if got != tt.wantBypass {
				t.Fatalf("ClassifyBypass(%q, %v) bypass = %v, want %v", tt.cmd, tt.args, got, tt.wantBypass)
			}
		})
	}
}

func TestClassifyBypassDBClients(t *testing.T) {
	tests := []struct {
		name       string
		cmd        string
		args       []string
		wantBypass bool
	}{
		// MUST bypass: an interactive REPL (bare, or connection args only), or a
		// form that would read stdin. Capturing these would break the session.
		{"mysql bare", "mysql", nil, true},
		{"mysql conn args only", "mysql", []string{"-u", "root", "app"}, true},
		{"mariadb bare", "mariadb", nil, true},
		{"psql bare", "psql", nil, true},
		{"psql dbname only", "psql", []string{"mydb"}, true},
		// psql -f - (and spellings) reads SQL from stdin: must bypass like a REPL.
		{"psql -f - reads stdin", "psql", []string{"-f", "-"}, true},
		{"psql --file - reads stdin", "psql", []string{"--file", "-"}, true},
		{"psql --file=- reads stdin", "psql", []string{"--file=-"}, true},
		{"psql -f- attached stdin", "psql", []string{"-f-"}, true},
		{"psql -c with -f - still bypasses (stdin hazard)", "psql", []string{"-c", "SELECT 1", "-f", "-"}, true},
		{"redis-cli bare", "redis-cli", nil, true},
		{"redis-cli conn args only", "redis-cli", []string{"-h", "db", "-p", "6379"}, true},
		{"redis-cli -x reads stdin", "redis-cli", []string{"-x", "SET", "k"}, true},
		{"redis-cli --pipe reads stdin", "redis-cli", []string{"--pipe"}, true},
		// -r is repeat mode; -r -1 repeats forever. Both must stay bypassed so the
		// skipped value is never mistaken for a finite command keyword.
		{"redis-cli -r -1 infinite repeat", "redis-cli", []string{"-r", "-1", "PING"}, true},
		{"redis-cli -r finite repeat still bypassed", "redis-cli", []string{"-r", "5", "GET", "k"}, true},
		// streaming / blocking commands never return on their own.
		{"redis-cli MONITOR streams forever", "redis-cli", []string{"MONITOR"}, true},
		{"redis-cli SUBSCRIBE blocks", "redis-cli", []string{"SUBSCRIBE", "ch"}, true},
		{"redis-cli lowercase psubscribe blocks", "redis-cli", []string{"psubscribe", "ch.*"}, true},
		{"redis-cli BLPOP blocking pop", "redis-cli", []string{"BLPOP", "q", "0"}, true},
		{"redis-cli -h host then MONITOR", "redis-cli", []string{"-h", "db", "MONITOR"}, true},
		// ssh stays bypassed: its command is a bare positional that may be an
		// interactive or long-running remote process, with no clean one-shot signal.
		{"ssh remote command", "ssh", []string{"host", "cat", "/etc/passwd"}, true},
		{"ssh interactive shell", "ssh", []string{"host"}, true},

		// MUST NOT bypass: a finite one-shot query (capture + scrub the output).
		{"mysql -e", "mysql", []string{"-e", "SELECT 1"}, false},
		{"mysql --execute=", "mysql", []string{"--execute=SELECT 1"}, false},
		{"mysql -e attached", "mysql", []string{"-eSELECT 1"}, false},
		{"mysql -e with conn args", "mysql", []string{"-u", "root", "-e", "SELECT 1"}, false},
		{"mariadb -e", "mariadb", []string{"-e", "SELECT 1"}, false},
		{"psql -c", "psql", []string{"-c", "SELECT 1"}, false},
		{"psql --command=", "psql", []string{"--command=SELECT 1"}, false},
		{"psql -l list", "psql", []string{"-l"}, false},
		{"psql -f file", "psql", []string{"-f", "seed.sql"}, false},
		{"psql -f attached real file", "psql", []string{"-fseed.sql"}, false},
		{"psql --file= real file", "psql", []string{"--file=seed.sql"}, false},
		{"redis-cli GET", "redis-cli", []string{"GET", "mykey"}, false},
		{"redis-cli conn args then command", "redis-cli", []string{"-h", "db", "PING"}, false},
		{"redis-cli -n then command", "redis-cli", []string{"-n", "0", "GET", "k"}, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, reason := ClassifyBypass(tt.cmd, tt.args)
			if got != tt.wantBypass {
				t.Fatalf("ClassifyBypass(%q, %v) bypass = %v (%q), want %v", tt.cmd, tt.args, got, reason, tt.wantBypass)
			}
		})
	}
}
