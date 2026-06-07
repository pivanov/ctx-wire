package main

import (
	"testing"

	"ctx-wire/internal/shim"
)

func TestShimDecide(t *testing.T) {
	walkAgent := func() (bool, string) { return true, "claude" }
	walkBrowser := func() (bool, string) { return true, "" } // wire-only, e.g. agent-browser
	walkNone := func() (bool, string) { return false, "" }

	cases := []struct {
		name   string
		env    shimEnv
		walk   func() (bool, string)
		action shimAction
		agent  string
	}{
		{"disabled", shimEnv{disable: true}, walkAgent, shimPassthrough, ""},
		{"shims0", shimEnv{shims0: true}, walkAgent, shimPassthrough, ""},
		{"depth cap wins over force", shimEnv{depth: shim.DepthCap, shims1: true}, walkAgent, shimPassthrough, ""},
		{"depth cap wins over agent env", shimEnv{depth: shim.DepthCap, agentEnv: "claude"}, walkAgent, shimPassthrough, ""},
		{"agent env wires", shimEnv{agentEnv: "codex"}, walkNone, shimWire, "codex"},
		{"force shims1", shimEnv{shims1: true}, walkNone, shimWire, ""},
		{"force agentShims", shimEnv{agentShims: true}, walkNone, shimWire, ""},
		{"walk agent", shimEnv{}, walkAgent, shimWire, "claude"},
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
