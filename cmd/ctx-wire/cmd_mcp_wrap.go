package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/mcpcompress"
	"ctx-wire/internal/paths"
	"ctx-wire/internal/scrub"
)

// mcpRawSpoolCap bounds the per-session raw-recovery spool. MCP snapshots carry
// page text and URLs, so the recovery copy is capped; once it is reached, results
// are forwarded uncompressed rather than grow the spool without bound.
const mcpRawSpoolCap = 64 << 20 // 64 MiB

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
				"ctx-wire mcp-wrap [--compress] -- <server> [args]",
				"ctx-wire mcp-wrap install [--compress] <server> [--config PATH]",
				"ctx-wire mcp-wrap uninstall <server> [--config PATH]",
			},
			summary: "Transparently relay a stdio MCP server, measure per-tool token cost, and optionally compress verbose results.",
			notes: []string{
				"It forwards every JSON-RPC message unchanged and records the result size of each tools/call, so you can see where MCP tokens go. A per-tool summary is written under the data dir and printed to stderr when the session ends.",
				"`--compress` reduces verbose accessibility snapshots (chrome-devtools take_snapshot and Playwright browser_snapshot) before they reach the agent. The reduction is subtractive (drops page-chrome subtrees and redundant text; never renumbers a ref), the raw result is spooled locally for recovery, and any reduction error falls back to the untouched result.",
				"`install <server>` rewrites that server's entry in your MCP config (default ~/.claude.json) to launch through mcp-wrap; add `--compress` to turn on snapshot compression for it; `uninstall` reverts either form. Both back up the config and need an agent restart to take effect.",
			},
			examples: []string{
				"ctx-wire mcp-wrap -- npx @playwright/mcp@latest",
				"ctx-wire mcp-wrap --compress -- npx chrome-devtools-mcp@latest",
				"ctx-wire mcp-wrap install chrome-devtools",
			},
		})
		return 0
	}

	// install/uninstall rewrite the MCP config rather than relaying.
	if len(args) > 0 && (args[0] == "install" || args[0] == "uninstall") {
		name, configPath := "", ""
		compress := false
		for i, rest := 1, args; i < len(rest); i++ {
			switch a := rest[i]; {
			case a == "--compress":
				compress = true
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
			usageLine(os.Stderr, "ctx-wire mcp-wrap "+args[0]+" [--compress] <server> [--config PATH]")
			return 2
		}
		if args[0] == "install" {
			return mcpWrapInstall(configPath, name, compress)
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

	compress := false
	pre := args
	if sep >= 0 {
		pre = args[:sep]
	}
	for _, a := range pre {
		if a == "--compress" {
			compress = true
		}
	}
	return runMCPWrapRelay(os.Stdin, os.Stdout, server, compress)
}

// runMCPWrapRelay is the relay core behind `ctx-wire mcp-wrap -- <server>`,
// parameterized over the agent-side pipes so the process-level robustness
// properties (child crash without hang, EOF propagation, exit-code passthrough,
// byte-verbatim forwarding) are provable in tests against real subprocesses.
// stdin must be a real file (os.Stdin, an os.Pipe end): Close() unblocking a
// parked Read is what prevents a hang when the child dies while the agent is
// idle.
func runMCPWrapRelay(stdin io.ReadCloser, stdout io.Writer, server []string, compress bool) int {
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

	m := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: compress, spoolCap: mcpRawSpoolCap}
	if compress {
		m.openSpool()
		if m.spool == nil {
			fmt.Fprintln(os.Stderr, "ctx-wire mcp-wrap: --compress could not open a private raw-recovery spool; results will be forwarded uncompressed.")
		}
		defer m.closeSpool()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	// agent -> server: forward verbatim, note tools/call request ids and names.
	go func() {
		defer wg.Done()
		defer childIn.Close()
		relayMCP(stdin, childIn, m.onAgentMsg)
	}()
	// server -> agent: measure result content; with --compress, reduce snapshot
	// results before forwarding (default forwards verbatim).
	go func() {
		defer wg.Done()
		relayMCPTransform(childOut, stdout, m.serverMsg)
		// The child's stdout closed (it exited). Unblock the agent->server reader
		// that may be parked on stdin, so the session does not hang if the child
		// crashes while the agent is idle (otherwise wg.Wait blocks forever).
		_ = stdin.Close()
	}()
	wg.Wait()
	werr := cmd.Wait()

	m.report()
	// Propagate the wrapped server's exit status so the host sees a failed server
	// as a failure, not a successful wrapper.
	return mcpChildExitCode(werr)
}

// mcpChildExitCode maps a cmd.Wait() error to an exit code: 0 on success, the
// child's own code when it exited non-zero, and 1 for any other failure (signal,
// could-not-run).
func mcpChildExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
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

// relayMCPTransform is like relayMCP but the transform returns the bytes to
// forward, so the server->agent direction can compress a result. transform must
// return valid JSON-RPC (or the original line). MCP stdio framing is one JSON
// message per line.
func relayMCPTransform(src io.Reader, dst io.Writer, transform func([]byte) []byte) {
	r := bufio.NewReader(src)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := dst.Write(transform(line)); werr != nil {
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

	compress   bool     // --compress: reduce snapshot result text before forwarding
	compRaw    int      // bytes of snapshot text seen (compressed path)
	compOut    int      // bytes emitted after reduction
	compCalls  int      // results actually reduced
	spool      *os.File // scrubbed raw results spooled here for recovery (local, private)
	spoolPath  string   // path shown in the compressed-result note
	spoolBytes int      // bytes written to the spool so far (cap guard)
	spoolCap   int      // max bytes to spool this session
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

// serverMsg measures the raw result and, when --compress is on, reduces a
// snapshot result before forwarding. It always measures the RAW size; the
// reduction is best-effort with a hard fallback to the untouched raw line.
func (m *mcpMeasure) serverMsg(line []byte) []byte {
	m.onServerMsg(line)
	if !m.compress {
		return line
	}
	red, rawN, outN, ok := m.reduceLine(line)
	if !ok {
		return line
	}
	// Only hand the agent the compressed form once the untouched (secret-scrubbed)
	// raw is recorded for recovery. If the spool is unavailable or full, forward the
	// raw result: never give the agent a compressed snapshot it cannot recover.
	if !m.spoolRaw([]byte(scrub.Scrub(string(line)))) {
		return line
	}
	m.mu.Lock()
	m.compRaw += rawN
	m.compOut += outN
	m.compCalls++
	m.mu.Unlock()
	// Surface the compression savings in the gain ledger (source="mcp"). Until now
	// the --compress relay reduced output but recorded nothing, so these savings
	// were invisible to `ctx-wire gain` and the local ledger , the read-ceiling
	// gain-gap, applied to the MCP surface. Recorded only after the raw is spooled,
	// so a recorded saving is always recoverable.
	gain.RecordMCP("mcp snapshot", "snapshot", "mcp-compress", agent.Current(), rawN, outN)
	return red
}

// reduceLine reduces snapshot text inside a JSON-RPC result, returning valid
// JSON-RPC. Fully fail-safe: any parse/marshal error or panic yields (nil,false)
// so the caller forwards the untouched raw line. The reducer self-gates
// (non-snapshot text reduces to itself), so only real a11y snapshots change.
func (m *mcpMeasure) reduceLine(line []byte) (out []byte, rawN, outN int, ok bool) {
	defer func() {
		if recover() != nil {
			out, rawN, outN, ok = nil, 0, 0, false
		}
	}()
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var msg map[string]any
	if dec.Decode(&msg) != nil {
		return nil, 0, 0, false
	}
	result, _ := msg["result"].(map[string]any)
	if result == nil {
		return nil, 0, 0, false
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return nil, 0, 0, false
	}
	changed := false
	for _, c := range content {
		item, _ := c.(map[string]any)
		if item == nil || item["type"] != "text" {
			continue
		}
		txt, _ := item["text"].(string)
		if txt == "" {
			continue
		}
		rawN += len(txt)
		red, dropped := mcpcompress.ReduceSnapshot(txt)
		if dropped > 0 {
			red += "\n[ctx-wire: snapshot compressed; " + strconv.Itoa(dropped) + " elements omitted, raw (scrubbed) spooled to " + m.spoolPath + "]"
			item["text"] = red
			changed = true
		}
		if s, sok := item["text"].(string); sok {
			outN += len(s)
		}
	}
	if !changed {
		return nil, 0, 0, false
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, 0, 0, false
	}
	return append(b, '\n'), rawN, outN, true
}

// spoolRaw appends the secret-scrubbed raw result so a compressed snapshot stays
// recoverable, and enforces the per-session cap. It returns false when the spool
// is unavailable or the cap would be exceeded, which is the caller's signal to
// forward the result uncompressed (never compress without a recovery path).
// Local-only.
func (m *mcpMeasure) spoolRaw(line []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.spool == nil || m.spoolBytes+len(line) > m.spoolCap {
		return false
	}
	n, err := m.spool.Write(line)
	m.spoolBytes += n
	return err == nil
}

// openSpool creates the per-session raw-result spool (local, 0600) used for
// recovery when --compress is on. Best-effort: a failure just means no spool.
func (m *mcpMeasure) openSpool() {
	base, err := paths.DataHome()
	if err != nil {
		return
	}
	dir := filepath.Join(base, paths.AppName)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	// CreateTemp gives each session its own exclusive file: two relays started
	// in the same second must never share a spool (intermixed raw results and a
	// jointly-bypassed per-session cap). The timestamp stays in the name for
	// human browsing; the random suffix guarantees uniqueness.
	f, err := os.CreateTemp(dir, "mcp-spool-"+time.Now().UTC().Format("20060102-150405")+"-*.jsonl")
	if err != nil {
		return
	}
	m.spool = f
	m.spoolPath = f.Name()
}

func (m *mcpMeasure) closeSpool() {
	if m.spool != nil {
		_ = m.spool.Close()
	}
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

	mode := "measurement (no compression)"
	if m.compress {
		mode = "measurement + compression"
	}
	var b []byte
	b = append(b, fmt.Sprintf("ctx-wire mcp-wrap %s\n%-28s %8s %12s %12s\n", mode, "tool", "calls", "bytes", "~tokens")...)
	var totalBytes int
	for _, n := range names {
		st := m.tools[n]
		totalBytes += st.resultByte
		b = append(b, fmt.Sprintf("%-28s %8d %12d %12d\n", n, st.calls, st.resultByte, st.resultByte/4)...)
	}
	b = append(b, fmt.Sprintf("%-28s %8s %12d %12d\n", "TOTAL", "", totalBytes, totalBytes/4)...)

	if m.compress {
		saved := m.compRaw - m.compOut
		pct := 0.0
		if m.compRaw > 0 {
			pct = 100 * float64(saved) / float64(m.compRaw)
		}
		b = append(b, fmt.Sprintf("\ncompression: %d snapshot result(s), %d -> %d bytes (%.1f%% saved, ~%d tokens)\n",
			m.compCalls, m.compRaw, m.compOut, pct, saved/4)...)
		if m.spoolPath != "" {
			b = append(b, fmt.Sprintf("scrubbed raw results recoverable: %s\n", m.spoolPath)...)
		}
	}

	fmt.Fprint(os.Stderr, "\n"+string(b))
	if base, err := paths.DataHome(); err == nil {
		dir := filepath.Join(base, paths.AppName)
		if os.MkdirAll(dir, 0o700) == nil {
			name := filepath.Join(dir, "mcp-measure-"+time.Now().UTC().Format("20060102-150405")+".txt")
			_ = os.WriteFile(name, b, 0o600)
			fmt.Fprintf(os.Stderr, "(saved to %s)\n", name)
			m.appendCumulative(dir)
		}
	}
}

// appendCumulative appends this session's per-tool measurements to a cumulative
// JSONL log (mcp-measure.jsonl) so the Phase-0 dataset accumulates across
// sessions instead of scattering into one file per run. Sum it across all
// sessions with e.g. `jq -s 'group_by(.tool) | map({tool: .[0].tool, calls:
// (map(.calls)|add), bytes: (map(.bytes)|add)})'`. Caller holds m.mu;
// best-effort, a write failure never affects the relay.
func (m *mcpMeasure) appendCumulative(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "mcp-measure.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	enc := json.NewEncoder(f)
	for name, st := range m.tools {
		_ = enc.Encode(struct {
			TS     string `json:"ts"`
			Tool   string `json:"tool"`
			Calls  int    `json:"calls"`
			Bytes  int    `json:"bytes"`
			Tokens int    `json:"approx_tokens"`
		}{ts, name, st.calls, st.resultByte, st.resultByte / 4})
	}
}
