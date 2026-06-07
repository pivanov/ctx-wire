package main

import (
	"bytes"
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
