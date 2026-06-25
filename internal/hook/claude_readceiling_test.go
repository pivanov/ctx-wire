package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bigText(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line content for the read-ceiling spike test"
	}
	return strings.Join(lines, "\n")
}

func TestCeilingText(t *testing.T) {
	if _, changed := ceilingText("a\nb\nc", "ctx-wire fetch deadbeef"); changed {
		t.Fatal("short text should not be ceilinged")
	}
	out, changed := ceilingText(bigText(300), "ctx-wire fetch deadbeef")
	if !changed {
		t.Fatal("300-line text should be ceilinged")
	}
	if !strings.Contains(out, "ctx-wire read-ceiling:") {
		t.Fatalf("missing marker: %q", out[:80])
	}
	if !strings.Contains(out, "ctx-wire fetch deadbeef") {
		t.Fatal("marker missing the recovery hint")
	}
	// head + marker + tail
	if got, want := strings.Count(out, "\n")+1, ceilHeadLines+1+ceilTailLines; got != want {
		t.Fatalf("ceilinged line count = %d, want %d", got, want)
	}
}

func TestExtractReadText(t *testing.T) {
	// string shape round-trips as a string
	txt, rewrap, ok := extractReadText(json.RawMessage(`"hello\nworld"`))
	if !ok || txt != "hello\nworld" {
		t.Fatalf("string shape: ok=%v txt=%q", ok, txt)
	}
	if got := string(rewrap("X")); got != `"X"` {
		t.Fatalf("string rewrap = %s, want \"X\"", got)
	}
	// object shape: only the text field changes, siblings survive
	txt, rewrap, ok = extractReadText(json.RawMessage(`{"type":"text","content":"body","totalLines":3}`))
	if !ok || txt != "body" {
		t.Fatalf("object shape: ok=%v txt=%q", ok, txt)
	}
	var m map[string]any
	if err := json.Unmarshal(rewrap("NEW"), &m); err != nil {
		t.Fatalf("rewrap not valid json: %v", err)
	}
	if m["content"] != "NEW" || m["type"] != "text" || m["totalLines"].(float64) != 3 {
		t.Fatalf("object rewrap dropped siblings or missed content: %v", m)
	}
	// Shape C: the verified Claude Read shape, content nested under file,
	// every sibling (top-level and file-level) preserved.
	real := `{"type":"text","file":{"filePath":"/x.go","content":"body","numLines":1,"startLine":1,"totalLines":1}}`
	txt, rewrap, ok = extractReadText(json.RawMessage(real))
	if !ok || txt != "body" {
		t.Fatalf("nested file shape: ok=%v txt=%q", ok, txt)
	}
	var nested map[string]any
	if err := json.Unmarshal(rewrap("NEW"), &nested); err != nil {
		t.Fatalf("nested rewrap not valid json: %v", err)
	}
	file, _ := nested["file"].(map[string]any)
	if nested["type"] != "text" || file == nil || file["content"] != "NEW" ||
		file["filePath"] != "/x.go" || file["numLines"].(float64) != 1 || file["totalLines"].(float64) != 1 {
		t.Fatalf("nested rewrap dropped a key or missed content: %v", nested)
	}

	// unknown shape fails open
	if _, _, ok := extractReadText(json.RawMessage(`[1,2,3]`)); ok {
		t.Fatal("array shape should fail open (ok=false)")
	}
}

func postPayload(t *testing.T, resp any) claudeInput {
	t.Helper()
	rb, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	return claudeInput{
		ToolName:      "Read",
		HookEventName: "PostToolUse",
		ToolInput:     json.RawMessage(`{"file_path":"/tmp/x.txt"}`),
		ToolResponse:  rb,
	}
}

func TestClaudePostToolUse_Modes(t *testing.T) {
	t.Setenv("CTX_WIRE_SPIKE_DIR", t.TempDir())
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	in := postPayload(t, bigText(300))

	// off and measure never emit (native output stands).
	for _, m := range []string{"off", "measure"} {
		t.Setenv("CTX_WIRE_READ_CEILING", m)
		var buf bytes.Buffer
		if err := claudePostToolUse(in, &buf); err != nil {
			t.Fatal(err)
		}
		if buf.Len() != 0 {
			t.Fatalf("mode %q emitted %d bytes, want 0", m, buf.Len())
		}
	}

	// on -> updatedToolOutput with the ceilinged body + recovery handle.
	t.Setenv("CTX_WIRE_READ_CEILING", "on")
	var rw bytes.Buffer
	if err := claudePostToolUse(in, &rw); err != nil {
		t.Fatal(err)
	}
	var out claudePostOutput
	if err := json.Unmarshal(rw.Bytes(), &out); err != nil {
		t.Fatalf("on-mode output not valid json: %v", err)
	}
	if out.HookSpecificOutput.HookEventName != "PostToolUse" {
		t.Fatalf("event = %q", out.HookSpecificOutput.HookEventName)
	}
	var body string
	if err := json.Unmarshal(out.HookSpecificOutput.UpdatedToolOutput, &body); err != nil {
		t.Fatalf("updatedToolOutput not a string for string-shape input: %v", err)
	}
	if !strings.Contains(body, "ctx-wire read-ceiling:") {
		t.Fatal("rewritten body missing ceiling marker")
	}
	if !strings.Contains(body, "ctx-wire fetch ") {
		t.Fatal("on-mode marker missing recovery handle")
	}
	if len(body) >= len(bigText(300)) {
		t.Fatal("rewrite did not shrink the body")
	}
}

func TestClaudePostToolUse_RecordsGain(t *testing.T) {
	t.Setenv("CTX_WIRE_SPIKE_DIR", t.TempDir())
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	gainFile := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv("CTX_WIRE_GAIN_FILE", gainFile)
	t.Setenv("CTX_WIRE_GAIN", "1")

	// on mode rewrites -> one gain row under program "Read" with a real saving.
	t.Setenv("CTX_WIRE_READ_CEILING", "on")
	if err := claudePostToolUse(postPayload(t, bigText(300)), io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(gainFile)
	if err != nil {
		t.Fatalf("gain log not written: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"command":"Read"`) || !strings.Contains(s, `"filter":"read-ceiling"`) {
		t.Fatalf("gain row missing Read/read-ceiling: %s", s)
	}
	if !strings.Contains(s, `"saved_bytes"`) || strings.Contains(s, `"saved_bytes":0`) {
		t.Fatalf("gain row recorded no saving: %s", s)
	}

	// measure mode reshapes nothing, so it must record NO gain.
	if err := os.Truncate(gainFile, 0); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CTX_WIRE_READ_CEILING", "measure")
	if err := claudePostToolUse(postPayload(t, bigText(300)), io.Discard); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(gainFile); len(data) != 0 {
		t.Fatalf("measure mode recorded gain it should not: %s", data)
	}
}

func TestClaudePostToolUse_ScrubsEmitted(t *testing.T) {
	t.Setenv("CTX_WIRE_SPIKE_DIR", t.TempDir())
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	t.Setenv("CTX_WIRE_READ_CEILING", "on")
	// A JWT on the first line lives in the kept head; the emitted ceiling must
	// scrub it (native Read would have leaked it; ctx-wire must not).
	body := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.dozjgNryP4J3jVmNHl0w5N\n" + bigText(300)
	var rw bytes.Buffer
	if err := claudePostToolUse(postPayload(t, body), &rw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rw.Bytes(), []byte("eyJhbGciOiJIUzI1NiJ9")) {
		t.Fatal("emitted ceiling leaked a secret that should be scrubbed")
	}
}

func TestClaudePostToolUse_IgnoresNonRead(t *testing.T) {
	t.Setenv("CTX_WIRE_READ_CEILING", "1")
	in := claudeInput{ToolName: "Bash", HookEventName: "PostToolUse", ToolResponse: json.RawMessage(`"x"`)}
	var buf bytes.Buffer
	if err := claudePostToolUse(in, &buf); err != nil || buf.Len() != 0 {
		t.Fatalf("non-Read PostToolUse should be a no-op: err=%v bytes=%d", err, buf.Len())
	}
}

func TestClaudePostToolUse_FailClosedEmitsNothing(t *testing.T) {
	t.Setenv("CTX_WIRE_SPIKE_DIR", t.TempDir())
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	t.Setenv("CTX_WIRE_READ_CEILING", "on")

	// Override the seam to force a scrub failure (fail-closed).
	orig := scrubFailClosed
	scrubFailClosed = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { scrubFailClosed = orig })

	// Build a large unranged Read input that would otherwise be reshaped.
	var buf bytes.Buffer
	if err := claudePostToolUse(postPayload(t, bigText(300)), &buf); err != nil {
		t.Fatalf("fail-closed should not return an error, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("fail-closed should emit nothing, got %d bytes: %s", buf.Len(), buf.String())
	}
}
