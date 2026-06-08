package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ctx-wire/internal/paths"
)

// cmdMCPWrap is the MCP Phase 0 measurement spike. It sits transparently between
// an agent and a stdio MCP server, relays every JSON-RPC message byte-for-byte,
// and only MEASURES the per-tool token cost of tools/call results. No
// compression: this exists to find where the token fat actually is before any
// compression is built (the gate in the MCP RFC).
//
// Usage: point the agent's MCP config at `ctx-wire mcp-wrap -- <server> [args]`.
func cmdMCPWrap(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage: []string{
				"ctx-wire mcp-wrap -- <server> [args]",
				"ctx-wire mcp-wrap install <server> [--config PATH]",
				"ctx-wire mcp-wrap uninstall <server> [--config PATH]",
			},
			summary: "Transparently relay a stdio MCP server and measure per-tool token cost (no compression).",
			notes: []string{
				"It forwards every JSON-RPC message unchanged and records the result size of each tools/call, so you can see where MCP tokens go before building compression. A per-tool summary is written under the data dir and printed to stderr when the session ends.",
				"`install <server>` rewrites that server's entry in your MCP config (default ~/.claude.json) to launch through mcp-wrap; `uninstall` reverts it. Both back up the config and need an agent restart to take effect.",
			},
			examples: []string{
				"ctx-wire mcp-wrap -- npx @playwright/mcp@latest",
				"ctx-wire mcp-wrap install chrome-devtools",
			},
		})
		return 0
	}

	// install/uninstall rewrite the MCP config rather than relaying.
	if len(args) > 0 && (args[0] == "install" || args[0] == "uninstall") {
		name, configPath := "", ""
		for i, rest := 1, args; i < len(rest); i++ {
			switch a := rest[i]; {
			case a == "--config":
				if i+1 < len(rest) {
					i++
					configPath = rest[i]
				}
			case strings.HasPrefix(a, "--config="):
				configPath = strings.TrimPrefix(a, "--config=")
			case strings.HasPrefix(a, "--"):
				fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap %s: unknown flag %q\n", args[0], a)
				return 2
			default:
				name = a
			}
		}
		if name == "" {
			usageLine(os.Stderr, "ctx-wire mcp-wrap "+args[0]+" <server> [--config PATH]")
			return 2
		}
		if args[0] == "install" {
			return mcpWrapInstall(configPath, name)
		}
		return mcpWrapUninstall(configPath, name)
	}

	// Everything after `--` is the real server command.
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	server := args
	if sep >= 0 {
		server = args[sep+1:]
	}
	if len(server) == 0 {
		usageLine(os.Stderr, "ctx-wire mcp-wrap -- <server> [args]")
		return 2
	}

	cmd := exec.Command(server[0], server[1:]...)
	cmd.Stderr = os.Stderr
	childIn, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: %v\n", err)
		return 1
	}
	childOut, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: %v\n", err)
		return 1
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: cannot start %q: %v\n", server[0], err)
		return 1
	}

	m := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}}

	var wg sync.WaitGroup
	wg.Add(2)
	// agent -> server: forward verbatim, note tools/call request ids and names.
	go func() {
		defer wg.Done()
		defer childIn.Close()
		relayMCP(os.Stdin, childIn, m.onAgentMsg)
	}()
	// server -> agent: forward verbatim, measure tools/call result content.
	go func() {
		defer wg.Done()
		relayMCP(childOut, os.Stdout, m.onServerMsg)
		// The child's stdout closed (it exited). Unblock the agent->server reader
		// that may be parked on os.Stdin, so the session does not hang if the child
		// crashes while the agent is idle (otherwise wg.Wait blocks forever).
		_ = os.Stdin.Close()
	}()
	wg.Wait()
	_ = cmd.Wait()

	m.report()
	return 0
}

// relayMCP copies newline-delimited JSON-RPC messages from src to dst byte for
// byte, calling hook on each message first (best-effort; the hook never alters
// or blocks forwarding). MCP stdio framing is one JSON message per line.
func relayMCP(src io.Reader, dst io.Writer, hook func([]byte)) {
	r := bufio.NewReader(src)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			hook(line)
			_, werr := dst.Write(line)
			if werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

type toolStat struct {
	calls      int
	resultByte int
}

type mcpMeasure struct {
	mu      sync.Mutex
	tools   map[string]*toolStat
	pending map[string]string // request id (raw json) -> tool name
}

type rpcMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
	Result *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
}

func (m *mcpMeasure) onAgentMsg(line []byte) {
	var msg rpcMsg
	if json.Unmarshal(line, &msg) != nil || msg.Method != "tools/call" || len(msg.ID) == 0 {
		return
	}
	m.mu.Lock()
	m.pending[string(msg.ID)] = msg.Params.Name
	m.mu.Unlock()
}

func (m *mcpMeasure) onServerMsg(line []byte) {
	var msg rpcMsg
	if json.Unmarshal(line, &msg) != nil || len(msg.ID) == 0 || msg.Result == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	name, ok := m.pending[string(msg.ID)]
	if !ok {
		return
	}
	delete(m.pending, string(msg.ID))
	// Text-only measurement: image/resource content has no Text and counts 0.
	// That is intentional for a token-cost spike (images are not text tokens), so
	// image-heavy tools are deliberately undercounted here.
	n := 0
	for _, c := range msg.Result.Content {
		n += len(c.Text)
	}
	st := m.tools[name]
	if st == nil {
		st = &toolStat{}
		m.tools[name] = st
	}
	st.calls++
	st.resultByte += n
}

// report writes a per-tool summary to the data dir and prints it to stderr, so
// the measurement survives the session.
func (m *mcpMeasure) report() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tools) == 0 {
		return
	}
	names := make([]string, 0, len(m.tools))
	for n := range m.tools {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		return m.tools[names[i]].resultByte > m.tools[names[j]].resultByte
	})

	var b []byte
	b = append(b, fmt.Sprintf("ctx-wire mcp-wrap measurement (no compression)\n%-28s %8s %12s %12s\n", "tool", "calls", "bytes", "~tokens")...)
	var totalBytes int
	for _, n := range names {
		st := m.tools[n]
		totalBytes += st.resultByte
		b = append(b, fmt.Sprintf("%-28s %8d %12d %12d\n", n, st.calls, st.resultByte, st.resultByte/4)...)
	}
	b = append(b, fmt.Sprintf("%-28s %8s %12d %12d\n", "TOTAL", "", totalBytes, totalBytes/4)...)

	fmt.Fprint(os.Stderr, "\n"+string(b))
	if base, err := paths.DataHome(); err == nil {
		dir := filepath.Join(base, paths.AppName)
		if os.MkdirAll(dir, 0o700) == nil {
			name := filepath.Join(dir, "mcp-measure-"+time.Now().UTC().Format("20060102-150405")+".txt")
			_ = os.WriteFile(name, b, 0o600)
			fmt.Fprintf(os.Stderr, "(saved to %s)\n", name)
		}
	}
}
