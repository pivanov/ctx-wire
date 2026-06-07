package agent

import "testing"

func TestMatchWire(t *testing.T) {
	cases := []struct {
		cmd   string
		agent string
		wire  bool
	}{
		{"/usr/bin/claude", "claude", true},
		{"node /opt/agent-browser/server.js", "", true}, // wire-only: route, do not attribute
		{"/Applications/Cursor.app/Contents/MacOS/Cursor", "cursor", true},
		{"code --gemini", "", false}, // flag token dropped, no bare agent word
		{"bash -lc ls", "", false},
	}
	for _, c := range cases {
		ag, w := matchWire(c.cmd)
		if ag != c.agent || w != c.wire {
			t.Errorf("matchWire(%q) = (%q,%v), want (%q,%v)", c.cmd, ag, w, c.agent, c.wire)
		}
	}
}

func TestDetectShimFrom(t *testing.T) {
	// agent-browser is the closest wire match, so it wires without attribution
	// even though claude is further up the chain.
	procs := map[int]procInfo{
		100: {ppid: 50, cmd: "git status"},
		50:  {ppid: 20, cmd: "node agent-browser"},
		20:  {ppid: 1, cmd: "claude"},
	}
	if wire, ag := detectShimFrom(100, procs); !wire || ag != "" {
		t.Errorf("closest=agent-browser: got (%v,%q), want (true,\"\")", wire, ag)
	}
	if wire, ag := detectShimFrom(20, procs); !wire || ag != "claude" {
		t.Errorf("claude ancestor: got (%v,%q), want (true,\"claude\")", wire, ag)
	}

	plain := map[int]procInfo{10: {ppid: 1, cmd: "bash"}}
	if wire, _ := detectShimFrom(10, plain); wire {
		t.Error("plain shell should not wire")
	}
}
