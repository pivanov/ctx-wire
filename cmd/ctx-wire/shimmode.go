package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/runner"
	"ctx-wire/internal/shim"
)

// shimAction is the outcome of the shim gating decision.
type shimAction int

const (
	shimPassthrough shimAction = iota
	shimWire
)

// shimEnv is the environment input to the gating decision. It carries only env
// state; the process walk is supplied separately so it can stay lazy.
type shimEnv struct {
	disable    bool   // CTX_WIRE_DISABLE_SHIMS truthy
	shims0     bool   // CTX_WIRE_SHIMS=0 (explicit off)
	shims1     bool   // CTX_WIRE_SHIMS=1 (force on)
	agentShims bool   // CTX_WIRE_AGENT_SHIMS truthy (force on)
	depth      int    // CTX_WIRE_SHIM_DEPTH
	agentEnv   string // CTX_WIRE_AGENT, normalized ("" if unset/invalid)
}

// shimDecide is the pure gating rule. It calls walk() only when the outcome
// genuinely depends on the process tree, so the hot paths (disabled, depth-cap,
// already-attributed, forced) never trigger a snapshot. walk returns
// (wire, agent): an attribution agent, a wire-only ancestor as (true, ""), or
// (false, ""). The order matters: the recursion backstop is checked before the
// force-ins so the fork-bomb guard always wins.
func shimDecide(e shimEnv, walk func() (wire bool, agent string)) (shimAction, string) {
	if e.disable || e.shims0 {
		return shimPassthrough, ""
	}
	if e.depth >= shim.DepthCap {
		return shimPassthrough, ""
	}
	// Force-on wins over the hook-capable passthrough below (debugging / opting
	// into broad coverage).
	if e.shims1 || e.agentShims {
		return shimWire, ""
	}
	// A hook/plugin-capable agent already rewrites model-visible commands, so the
	// shim must NOT also wire under it: that double-covers shell plumbing and
	// corrupts command substitutions (result=$(cat file)). This applies both to an
	// inherited CTX_WIRE_AGENT (a hook-wrapped command's subprocess) and to a
	// detected ancestor. Steering-only / opt-in agents have no auto-rewrite, so
	// the shim is their coverage and still wires.
	if e.agentEnv != "" {
		if agent.IsHookCapable(e.agentEnv) {
			return shimPassthrough, ""
		}
		return shimWire, e.agentEnv
	}
	if wire, ag := walk(); wire {
		if agent.IsHookCapable(ag) {
			return shimPassthrough, ""
		}
		return shimWire, ag
	}
	return shimPassthrough, ""
}

// truthy / falsy match the shell shim's accepted spellings.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func falsy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

func readShimEnv() shimEnv {
	depth, _ := strconv.Atoi(strings.TrimSpace(os.Getenv(shim.EnvDepth)))
	return shimEnv{
		disable:    truthy(os.Getenv(shim.EnvDisable)),
		shims0:     falsy(os.Getenv(shim.EnvForce)),
		shims1:     truthy(os.Getenv(shim.EnvForce)),
		agentShims: truthy(os.Getenv(shim.EnvAgent)),
		depth:      depth,
		agentEnv:   agent.Normalize(os.Getenv(agent.EnvName)),
	}
}

// cmdRunShim implements `ctx-wire run --shim <cmd> [args]`: the gating the Unix
// shell shim does, in Go. It resolves the real binary (never a shim), decides
// whether to wire, then either passes through byte-exact or filters via the
// normal run path.
func cmdRunShim(args []string) int {
	if len(args) == 0 {
		usageLine(os.Stderr, "ctx-wire run --shim <cmd> [args]")
		return 2
	}
	cmd := args[0]
	rest := args[1:]

	real, err := shim.ResolveRealExe(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire shim: %v\n", err)
		return 127
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	action, ag := shimDecide(readShimEnv(), agent.DetectShim)
	if action == shimPassthrough {
		return runner.RunRaw(ctx, real, rest)
	}

	// Wire: attribute (only when non-empty; preserve any existing valid value),
	// mark the shim, bump the recursion counter, then filter via the normal path.
	if ag != "" {
		os.Setenv(agent.EnvName, ag)
	}
	os.Setenv(shim.EnvName, cmd)
	os.Setenv(shim.EnvDepth, strconv.Itoa(readShimEnv().depth+1))

	reg, err := loadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire run: %v\n", err)
		return 1
	}
	code, err := runner.Run(ctx, reg, real, rest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}
