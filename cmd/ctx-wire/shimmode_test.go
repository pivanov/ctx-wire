package main

import (
	"testing"

	"ctx-wire/internal/shim"
)

func TestShimDecide(t *testing.T) {
	walkClaude := func() (bool, string) { return true, "claude" } // hook-capable
	walkCline := func() (bool, string) { return true, "cline" }   // steering-only
	walkBrowser := func() (bool, string) { return true, "" }      // wire-only, e.g. agent-browser
	walkNone := func() (bool, string) { return false, "" }

	cases := []struct {
		name   string
		env    shimEnv
		walk   func() (bool, string)
		action shimAction
		agent  string
	}{
		{"disabled", shimEnv{disable: true}, walkClaude, shimPassthrough, ""},
		{"shims0", shimEnv{shims0: true}, walkClaude, shimPassthrough, ""},
		{"depth cap wins over force", shimEnv{depth: shim.DepthCap, shims1: true}, walkClaude, shimPassthrough, ""},
		{"depth cap wins over agent env", shimEnv{depth: shim.DepthCap, agentEnv: "claude"}, walkClaude, shimPassthrough, ""},
		// A hook/plugin-capable agent (inherited env or detected) passes through:
		// its own rewrite covers model-visible commands, so the shim must not wrap.
		{"hook-capable agent env passes through", shimEnv{agentEnv: "codex"}, walkNone, shimPassthrough, ""},
		{"walk hook-capable passes through", shimEnv{}, walkClaude, shimPassthrough, ""},
		// Steering-only agents have no auto-rewrite, so the shim is their coverage.
		{"steering agent env wires", shimEnv{agentEnv: "cline"}, walkNone, shimWire, "cline"},
		{"walk steering wires", shimEnv{}, walkCline, shimWire, "cline"},
		// Force-on still wires even under a hook-capable agent (debug / broad opt-in).
		{"force wins over hook-capable agent env", shimEnv{agentEnv: "claude", shims1: true}, walkNone, shimWire, ""},
		{"force shims1", shimEnv{shims1: true}, walkNone, shimWire, ""},
		{"force agentShims", shimEnv{agentShims: true}, walkNone, shimWire, ""},
		{"walk wire-only no attribution", shimEnv{}, walkBrowser, shimWire, ""},
		{"walk none passthrough", shimEnv{}, walkNone, shimPassthrough, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			action, ag := shimDecide(c.env, c.walk)
			if action != c.action || ag != c.agent {
				t.Fatalf("shimDecide = (%v,%q), want (%v,%q)", action, ag, c.action, c.agent)
			}
		})
	}
}

// TestShimDecideLazyWalk pins that the process walk runs only when the
// environment does not already decide. CTX_WIRE_DISABLE_SHIMS=1 is set on every
// nested ctx-wire child, so a snapshot there would be pure waste.
func TestShimDecideLazyWalk(t *testing.T) {
	envDecided := []shimEnv{
		{disable: true},
		{shims0: true},
		{depth: shim.DepthCap},
		{depth: shim.DepthCap, shims1: true},
		{agentEnv: "claude"},
		{shims1: true},
		{agentShims: true},
	}
	for i, e := range envDecided {
		called := false
		shimDecide(e, func() (bool, string) { called = true; return true, "x" })
		if called {
			t.Errorf("case %d: walk ran but the environment should have decided", i)
		}
	}
	called := false
	shimDecide(shimEnv{}, func() (bool, string) { called = true; return false, "" })
	if !called {
		t.Error("walk should run when nothing in the environment decides")
	}
}
