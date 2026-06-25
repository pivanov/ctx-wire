package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ctx-wire/internal/filter"
)

// TestMain disables gain recording so MCP tests never write to the real
// telemetry log on the developer's machine.
func TestMain(m *testing.M) {
	os.Setenv("CTX_WIRE_GAIN", "0")
	os.Exit(m.Run())
}

// connect wires an in-memory client session to a freshly built ctx-wire MCP
// server and returns the client session for driving tool calls.
func connect(t *testing.T) (*mcp.ClientSession, context.Context) {
	t.Helper()
	reg, err := filter.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	srv := New(reg, "test")
	serverT, clientT := mcp.NewInMemoryTransports()

	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}

func TestMCPListsRunCommand(t *testing.T) {
	cs, ctx := connect(t)
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var found bool
	for _, tool := range res.Tools {
		if tool.Name == "run_command" {
			found = true
		}
	}
	if !found {
		t.Errorf("run_command tool not advertised; got %d tools", len(res.Tools))
	}
}

func TestMCPRunCommandFiltersAndScrubs(t *testing.T) {
	cs, ctx := connect(t)

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "run_command",
		Arguments: runInput{
			Command: "printf",
			Args:    []string{"deploy token ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ok\n"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool reported error: %+v", res.Content)
	}

	text := contentText(res)
	if !strings.Contains(text, "[REDACTED]") {
		t.Errorf("expected secret redaction in tool output, got: %q", text)
	}
	if strings.Contains(text, "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Errorf("secret leaked through MCP tool output: %q", text)
	}
}

func TestMCPRunCommandPropagatesExitCode(t *testing.T) {
	cs, ctx := connect(t)
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "run_command",
		Arguments: runInput{Command: "sh", Args: []string{"-c", "exit 5"}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(contentText(res), "\"exit_code\":5") {
		t.Errorf("expected exit_code 5 in structured output, got: %q", contentText(res))
	}
}

func TestMCPListsReadFile(t *testing.T) {
	cs, ctx := connect(t)
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var found bool
	for _, tool := range res.Tools {
		if tool.Name == "read_file" {
			found = true
		}
	}
	if !found {
		t.Errorf("read_file tool not advertised; got %d tools", len(res.Tools))
	}
}

func TestReadFileScrubsAndCaps(t *testing.T) {
	reg, err := filter.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	var sb strings.Builder
	sb.WriteString("token ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa here\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out, truncated, err := readFile(reg, p, 10)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if strings.Contains(out, "ghp_aaaa") {
		t.Error("secret leaked through read_file")
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Error("expected secret redaction")
	}
	if !truncated {
		t.Error("expected truncated=true with max_lines=10")
	}
	if got := strings.Count(out, "line "); got > 10 {
		t.Errorf("max_lines=10 not honored, got %d content lines", got)
	}
}

func TestReadFileMissing(t *testing.T) {
	reg, err := filter.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	if _, _, err := readFile(reg, filepath.Join(t.TempDir(), "nope.txt"), 0); err == nil {
		t.Error("expected error for a missing file")
	}
}

// contentText concatenates the text content blocks of a tool result.
func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestReadFileJSONIntegrity is the S3 fix: complete JSON read through the MCP
// read_file tool must never be cut by the cat filter's line caps or the
// maxLines split, which would hand the agent invalid JSON (rtk #2295). Four
// shapes pin it: minified JSON over the cat filter's 500-char line cap survives
// whole; pretty JSON survives even with a tiny maxLines; non-JSON still caps;
// oversize JSON gets a marker, never a mid-structure cut.
func TestReadFileJSONIntegrity(t *testing.T) {
	reg, err := filter.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mustParse := func(label, s string) {
		t.Helper()
		var v any
		if json.Unmarshal([]byte(s), &v) != nil {
			t.Fatalf("%s: output is not valid JSON:\n%q", label, s)
		}
	}

	// 1. Minified one-line JSON well over the cat filter's truncate_lines_at=500.
	keys := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		keys = append(keys, fmt.Sprintf("%q:%q", fmt.Sprintf("key_%02d", i), fmt.Sprintf("value-%02d-padding", i)))
	}
	minified := "{" + strings.Join(keys, ",") + "}"
	if len(minified) <= 500 {
		t.Fatalf("test setup: minified JSON must exceed 500 chars, got %d", len(minified))
	}
	out, truncated, err := readFile(reg, write("min.json", minified), 0)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	mustParse("minified", out)
	if truncated {
		t.Error("minified JSON under the cap must not report truncated")
	}

	// 2. Pretty JSON with a tiny maxLines: maxLines is deliberately ignored for
	// complete JSON (honoring it would re-break the document).
	pretty := "{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": [1, 2, 3],\n  \"d\": {\"e\": true}\n}"
	out, _, err = readFile(reg, write("pretty.json", pretty), 2)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	mustParse("pretty+maxLines", out)
	if strings.Contains(out, "more lines)") {
		t.Errorf("maxLines must not split complete JSON:\n%s", out)
	}

	// 3. Non-JSON still respects maxLines (no behavior change off the JSON path).
	var lines strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&lines, "log line %d\n", i)
	}
	out, truncated, err = readFile(reg, write("log.txt", lines.String()), 5)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if !truncated || !strings.Contains(out, "more lines)") {
		t.Errorf("non-JSON must still cap with maxLines:\n%s", out)
	}

	// 4. Oversize JSON -> size marker, never a mid-structure cut.
	defer func(old int) { filter.MaxJSONPassthrough = old }(filter.MaxJSONPassthrough)
	filter.MaxJSONPassthrough = 64
	out, truncated, err = readFile(reg, write("big.json", minified), 0)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if !truncated {
		t.Error("oversize JSON must report truncated")
	}
	if !strings.Contains(out, "JSON document omitted") {
		t.Errorf("oversize JSON must be replaced with a marker, got:\n%s", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("oversize JSON must not emit a mid-structure cut:\n%s", out)
	}
}

// TestReadFileRecordsMCPGain pins Phase-1 attribution: the MCP read_file tool
// filters/caps like `cat` but used to record no gain. It must now write a gain
// entry tagged source="mcp" (TestMain disables gain, so re-enable it here and
// redirect the ledger to a temp file).
func TestReadFileRecordsMCPGain(t *testing.T) {
	gf := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv("CTX_WIRE_GAIN_FILE", gf)
	t.Setenv("CTX_WIRE_GAIN", "1")

	reg, err := filter.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	p := filepath.Join(t.TempDir(), "noisy.txt")
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readFile(reg, p, 10); err != nil {
		t.Fatalf("readFile: %v", err)
	}
	data, err := os.ReadFile(gf)
	if err != nil {
		t.Fatalf("read gain ledger: %v", err)
	}
	if !strings.Contains(string(data), `"source":"mcp"`) {
		t.Fatalf("read_file did not record an mcp gain entry; ledger=%q", data)
	}
}
