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
