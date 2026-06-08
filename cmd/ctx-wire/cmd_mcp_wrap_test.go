package main

import (
	"bytes"
	"os/exec"
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
