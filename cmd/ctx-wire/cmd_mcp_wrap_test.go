package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPMeasurePairsAndCounts(t *testing.T) {
	m := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}}

	// Agent calls a tool (id 1); server returns content totaling 6 bytes.
	m.onAgentMsg([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_snapshot"}}`))
	m.onServerMsg([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"AAAA"},{"type":"text","text":"BB"}]}}`))

	st := m.tools["browser_snapshot"]
	if st == nil || st.calls != 1 || st.resultByte != 6 {
		t.Fatalf("measure = %+v, want 1 call / 6 bytes", st)
	}
	// A response with no matching pending request is ignored.
	m.onServerMsg([]byte(`{"jsonrpc":"2.0","id":99,"result":{"content":[{"type":"text","text":"X"}]}}`))
	if len(m.tools) != 1 {
		t.Errorf("an unpaired response must be ignored, tools=%v", m.tools)
	}
	// A non-tools/call message does not register.
	m.onAgentMsg([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if _, ok := m.pending["2"]; ok {
		t.Error("non-tools/call request must not be tracked")
	}
}

func TestMCPChildExitCode(t *testing.T) {
	if got := mcpChildExitCode(nil); got != 0 {
		t.Errorf("nil error -> %d, want 0", got)
	}
	// A real child that exits non-zero must have its code propagated.
	err := exec.Command("sh", "-c", "exit 3").Run()
	if got := mcpChildExitCode(err); got != 3 {
		t.Errorf("exit 3 -> %d, want 3", got)
	}
	// A non-exit error (could not start) maps to 1.
	err = exec.Command("/ctx-wire/definitely/not/a/real/binary").Run()
	if got := mcpChildExitCode(err); got != 1 {
		t.Errorf("could-not-run -> %d, want 1", got)
	}
}

// snapshotResultLine wraps a11y-snapshot text as a JSON-RPC tools/call result,
// marshaled the way a real server would frame it (so the test exercises the same
// parse path reduceLine takes on the wire).
func snapshotResultLine(t *testing.T, id int, text string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestReduceLineWireLevel is the wire-level contract for --compress: a real
// snapshot result is reduced to still-valid JSON-RPC with the id intact, kept
// refs survive byte-identical, dropped refs are gone, and anything that is not a
// reducible snapshot (plain text, non-JSON, a request with no result) falls back
// to the untouched raw line (ok=false) so the relay forwards it verbatim.
func TestReduceLineWireLevel(t *testing.T) {
	snap := strings.Join([]string{
		"## Page snapshot",
		`uid=1_1 RootWebArea "Test"`,
		`  uid=1_2 banner "Site header"`,
		`    uid=1_3 link "Logo"`,
		`    uid=1_4 navigation "Main"`,
		`      uid=1_5 link "Home"`,
		`  uid=1_6 main "Content"`,
		`    uid=1_7 heading "Title"`,
		`      uid=1_8 StaticText "Title"`,
		`  uid=1_9 contentinfo "Footer"`,
		`    uid=1_10 link "Privacy"`,
	}, "\n")

	m := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: true, spoolPath: "/tmp/spool.jsonl"}

	out, _, _, ok := m.reduceLine(snapshotResultLine(t, 7, snap))
	if !ok {
		t.Fatal("expected reduceLine to reduce a real snapshot")
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("reduced output is not valid JSON-RPC: %v\n%s", err, out)
	}
	if got := fmt.Sprint(parsed["id"]); got != "7" {
		t.Errorf("id not preserved through reduction: got %q want 7", got)
	}
	red := parsed["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	for _, keep := range []string{"uid=1_1 ", "uid=1_6 ", "uid=1_7 "} {
		if !strings.Contains(red, keep) {
			t.Errorf("kept ref %q missing from reduced text", keep)
		}
	}
	for _, drop := range []string{"uid=1_2 ", "uid=1_3 ", "uid=1_4 ", "uid=1_5 ", "uid=1_8 ", "uid=1_9 ", "uid=1_10 "} {
		if strings.Contains(red, drop) {
			t.Errorf("dropped ref %q still present in reduced text", drop)
		}
	}
	if !strings.Contains(red, "ctx-wire: snapshot compressed") {
		t.Error("expected the recovery note in reduced text")
	}

	// Anything that is not a reducible snapshot must fall back to raw (ok=false).
	for name, line := range map[string][]byte{
		"plain text result":  snapshotResultLine(t, 8, "just a plain tool result, no a11y tree here"),
		"non-JSON line":      []byte("this is not json at all\n"),
		"request, no result": []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x"}}`),
		"empty content":      []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`),
	} {
		if _, _, _, ok := m.reduceLine(line); ok {
			t.Errorf("%s: expected raw fallback (ok=false)", name)
		}
	}
}

// TestCompressSpoolScrubbedAndGated pins the privacy/recovery contract of
// --compress: (1) a snapshot is only compressed once a secret-scrubbed raw copy
// is recorded, so an injected token never lands on disk; (2) with no spool
// available the snapshot is forwarded RAW (we never compress without a recovery
// path); (3) past the per-session cap it is likewise forwarded raw.
func TestCompressSpoolScrubbedAndGated(t *testing.T) {
	secret := "ghp_" + strings.Repeat("A", 36) // a shape the scrubber redacts
	snap := strings.Join([]string{
		"## Page snapshot",
		`uid=1_1 RootWebArea "Repo"`,
		`  uid=1_2 banner "Site header"`, // dropped subtree -> guarantees reduction
		`    uid=1_3 link "Logo"`,
		`  uid=1_4 main "Content"`,
		`    uid=1_5 link "Token" url="https://x.test/?t=` + secret + `"`,
	}, "\n")
	line := snapshotResultLine(t, 5, snap)

	// (2) no spool -> forward raw, never compress.
	nospool := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: true, spoolCap: mcpRawSpoolCap}
	if got := nospool.serverMsg(line); !bytes.Equal(got, line) {
		t.Error("with --compress but no spool, a snapshot must be forwarded raw (no recovery -> no compression)")
	}

	// (1) real spool -> compresses, and the on-disk raw is scrubbed.
	dir := t.TempDir()
	path := dir + "/spool.jsonl"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	m := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: true, spool: f, spoolPath: path, spoolCap: mcpRawSpoolCap}
	out := m.serverMsg(line)
	f.Close()
	if bytes.Equal(out, line) {
		t.Fatal("expected the snapshot to be compressed when a spool is available")
	}
	disk, err := os.ReadFile(path)
	if err != nil || len(disk) == 0 {
		t.Fatalf("expected the raw result to be spooled: err=%v len=%d", err, len(disk))
	}
	if strings.Contains(string(disk), secret) {
		t.Errorf("SECRET LEAKED to the raw spool on disk:\n%s", disk)
	}
	if !strings.Contains(string(disk), "[REDACTED]") {
		t.Errorf("expected the spooled raw to be secret-scrubbed; got:\n%s", disk)
	}

	// (3) over the cap -> forward raw.
	f2, err := os.OpenFile(dir+"/spool2.jsonl", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	capped := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: true, spool: f2, spoolPath: dir + "/spool2.jsonl", spoolCap: 8}
	if got := capped.serverMsg(line); !bytes.Equal(got, line) {
		t.Error("over the spool cap, a snapshot must be forwarded raw")
	}
}

// TestServerMsgForwardsRawWhenSafe pins the relay contract: without --compress the
// line is byte-verbatim, and with --compress a result that is not a reducible
// snapshot is still forwarded verbatim (never dropped or corrupted).
func TestServerMsgForwardsRawWhenSafe(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"plain, not a snapshot"}]}}`)

	off := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: false}
	if got := off.serverMsg(line); !bytes.Equal(got, line) {
		t.Errorf("without --compress serverMsg must forward verbatim:\n got %q\nwant %q", got, line)
	}
	on := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: true}
	if got := on.serverMsg(line); !bytes.Equal(got, line) {
		t.Errorf("with --compress a non-snapshot result must still forward verbatim:\n got %q\nwant %q", got, line)
	}
}

func TestRelayMCPForwardsVerbatim(t *testing.T) {
	in := "line1\n{\"a\":1}\nlast-without-newline"
	var out bytes.Buffer
	hookCalls := 0
	relayMCP(strings.NewReader(in), &out, func([]byte) { hookCalls++ })

	if out.String() != in {
		t.Errorf("relay altered bytes:\n got %q\nwant %q", out.String(), in)
	}
	if hookCalls != 3 {
		t.Errorf("hook called %d times, want 3 (incl. the trailing partial line)", hookCalls)
	}
}

// TestOpenSpoolUniquePerSession pins the collision fix: two relays started in
// the same second must get distinct recovery spools, never a shared file.
func TestOpenSpoolUniquePerSession(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: true, spoolCap: mcpRawSpoolCap}
	b := &mcpMeasure{tools: map[string]*toolStat{}, pending: map[string]string{}, compress: true, spoolCap: mcpRawSpoolCap}
	a.openSpool()
	b.openSpool()
	defer a.closeSpool()
	defer b.closeSpool()
	if a.spool == nil || b.spool == nil {
		t.Fatalf("spools did not open: a=%v b=%v", a.spoolPath, b.spoolPath)
	}
	if a.spoolPath == b.spoolPath {
		t.Errorf("two sessions share one spool: %s", a.spoolPath)
	}
}

// TestServerMsgRecordsMCPGain pins the Phase-1 fix: the --compress relay used to
// reduce a snapshot but record NOTHING, so the savings were invisible to
// `ctx-wire gain`. serverMsg must now write a gain entry with source="mcp" once
// the raw is spooled for recovery.
func TestServerMsgRecordsMCPGain(t *testing.T) {
	gainFile := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv("CTX_WIRE_GAIN_FILE", gainFile)
	t.Setenv("CTX_WIRE_GAIN", "") // ensure recording is enabled

	snap := strings.Join([]string{
		"## Page snapshot",
		`uid=1_1 RootWebArea "Test"`,
		`  uid=1_2 banner "Site header"`,
		`    uid=1_3 link "Logo"`,
		`    uid=1_4 navigation "Main"`,
		`      uid=1_5 link "Home"`,
		`  uid=1_6 main "Content"`,
		`    uid=1_7 heading "Title"`,
		`      uid=1_8 StaticText "Title"`,
		`  uid=1_9 contentinfo "Footer"`,
		`    uid=1_10 link "Privacy"`,
	}, "\n")

	spool, err := os.CreateTemp(t.TempDir(), "spool-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	m := &mcpMeasure{
		tools: map[string]*toolStat{}, pending: map[string]string{},
		compress: true, spool: spool, spoolCap: 1 << 20, spoolPath: spool.Name(),
	}

	out := m.serverMsg(snapshotResultLine(t, 7, snap))
	if !strings.Contains(string(out), "ctx-wire: snapshot compressed") {
		t.Fatalf("expected the snapshot to be compressed; got %q", out)
	}

	data, err := os.ReadFile(gainFile)
	if err != nil {
		t.Fatalf("read gain ledger: %v", err)
	}
	if !strings.Contains(string(data), `"source":"mcp"`) {
		t.Fatalf("compress relay did not record an mcp gain entry; ledger=%q", data)
	}
	if !strings.Contains(string(data), `"mode":"mcp-compress"`) {
		t.Fatalf("expected mode=mcp-compress in the gain entry; ledger=%q", data)
	}
}
