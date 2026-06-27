package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The relay-robustness proof gating MCP auto-wrap: mcp-wrap sits on the
// critical path of every wrapped server, so before init may wrap anything
// automatically, these tests pin the properties that make a relay safe to
// interpose: it never hangs when the child dies, never alters bytes it does
// not positively reduce, propagates exit codes, and survives adversarial
// frames. All tests drive runMCPWrapRelay against REAL subprocesses through
// REAL pipes (the same kind os.Stdin is), so the Close-unblocks-Read
// mechanism is exercised, not simulated.

// relayResult runs the relay in a goroutine with a hang deadline; a relay that
// does not return is itself the failure being tested for.
func relayResult(t *testing.T, stdin *os.File, out *bytes.Buffer, server []string, compress bool) int {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir()) // keep report/spool writes in the sandbox
	done := make(chan int, 1)
	var mu sync.Mutex // out is written by the relay goroutine, read after done
	go func() {
		var buf lockedBuffer
		code := runMCPWrapRelay(stdin, &buf, server, compress)
		mu.Lock()
		out.Write(buf.Bytes())
		mu.Unlock()
		done <- code
	}()
	select {
	case code := <-done:
		mu.Lock()
		defer mu.Unlock()
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("relay HUNG: did not return after the child scenario completed")
		return -1
	}
}

// lockedBuffer makes the relay's writes race-free for the test reader.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.b.Bytes()...)
}

// TestRelayChildCrashWhileAgentIdleNoHang is the property that makes auto-wrap
// survivable: the child dies mid-stream while the agent side stays OPEN and
// silent (the common idle session). The relay must forward what was emitted,
// return the child's exit code, and above all RETURN, not park forever on the
// agent read.
func TestRelayChildCrashWhileAgentIdleNoHang(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close() // held open for the whole test: the agent never speaks and never hangs up
	var out bytes.Buffer
	code := relayResult(t, r, &out, []string{"sh", "-c", `printf '{"jsonrpc":"2.0","id":9,"result":{}}\npartial-no-newline'; exit 3`}, true)
	if code != 3 {
		t.Errorf("exit code = %d, want the child's 3", code)
	}
	got := out.String()
	if !strings.Contains(got, `"id":9`) || !strings.HasSuffix(got, "partial-no-newline") {
		t.Errorf("crash output not forwarded verbatim (incl. trailing partial line):\n%q", got)
	}
}

// TestRelayAgentEOFShutsDownCleanly: the agent hangs up (host shutdown). EOF
// must propagate to the child's stdin so a well-behaved child exits, and the
// relay with it.
func TestRelayAgentEOFShutsDownCleanly(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	go func() {
		w.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x"}}` + "\n")
		w.Close() // agent hangs up
	}()
	code := relayResult(t, r, &out, []string{"cat"}, false)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (cat exits cleanly on EOF)", code)
	}
	if !strings.Contains(out.String(), `"id":1`) {
		t.Errorf("frame did not round-trip through the child:\n%q", out.String())
	}
}

// TestRelayPassThroughByteValidity is the compression-on safety floor: with
// --compress active, everything that is NOT a positively-reduced snapshot must
// round-trip through a real child byte-for-byte: malformed JSON, non-JSON
// noise, huge lines (well past any default scanner limit), JSON-RPC frames of
// every shape, and a trailing partial line.
func TestRelayPassThroughByteValidity(t *testing.T) {
	huge := strings.Repeat("x", 2<<20) // 2 MiB single line, no internal newlines
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`not json at all`,
		`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"plain text result"}]}}`,
		`{"truncated":`,
		`{"jsonrpc":"2.0","method":"notifications/progress"}`,
		huge,
		`{"jsonrpc":"2.0","id":"str-id","result":{"content":[]}}`,
	}, "\n") + "\ntrailing-partial-without-newline"

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	go func() {
		w.WriteString(in)
		w.Close()
	}()
	code := relayResult(t, r, &out, []string{"cat"}, true) // compress ON
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if out.String() != in {
		t.Errorf("compress-on relay altered non-snapshot bytes: len got %d want %d", out.Len(), len(in))
	}
}

// TestRelayChildNotFound: a wrapped server whose binary is missing must fail
// fast with a diagnostic exit, never hang waiting on pipes that never open.
func TestRelayChildNotFound(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var out bytes.Buffer
	if code := relayResult(t, r, &out, []string{"definitely-not-a-real-binary-xyz"}, false); code != 1 {
		t.Errorf("exit code = %d, want 1 for an unstartable child", code)
	}
}

// TestRelaySnapshotStillCompressesEndToEnd: the positive control for the
// pass-through test, through the SAME real-subprocess path a snapshot result
// must come back reduced with the recovery note, proving compression is live
// (so byte-validity above is not vacuously passing with compression off).
func TestRelaySnapshotStillCompressesEndToEnd(t *testing.T) {
	// Enough droppable chrome that the reduction net-shrinks past the recovery
	// note, so the never-worse guard in serverMsg lets it compress (a tinier
	// snapshot would correctly net-expand and be forwarded raw).
	snap := strings.Join([]string{
		"## Page snapshot",
		`uid=1_1 RootWebArea "Repo"`,
		`  uid=1_2 banner "Site header"`,
		`    uid=1_3 link "Logo"`,
		`    uid=1_4 navigation "Global"`,
		`      uid=1_5 link "Home page"`,
		`      uid=1_6 link "Pull requests"`,
		`      uid=1_7 link "Issues list"`,
		`      uid=1_8 link "Marketplace and explore"`,
		`  uid=1_9 main "Content"`,
		`  uid=1_10 contentinfo "Footer"`,
		`    uid=1_11 link "Terms of service"`,
		`    uid=1_12 link "Privacy and cookies"`,
	}, "\n")
	// Build the frame with json.Marshal so the snapshot's inner quotes are
	// escaped properly; a hand-rolled sprintf here produces invalid JSON, which
	// the relay (correctly) refuses to touch.
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 4,
		"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": snap}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	line := string(payload)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	go func() {
		w.WriteString(line + "\n")
		w.Close()
	}()
	if code := relayResult(t, r, &out, []string{"cat"}, true); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := out.String()
	if !strings.Contains(got, "[ctx-wire: snapshot compressed;") {
		t.Fatalf("snapshot was not compressed on the real-subprocess path:\n%q", got)
	}
	if strings.Contains(got, "banner") {
		t.Errorf("dropped banner subtree still present after compression:\n%q", got)
	}
	if !strings.Contains(got, `"id":4`) {
		t.Errorf("compressed frame lost its JSON-RPC id:\n%q", got)
	}
}
