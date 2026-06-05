package rewrite

import (
	"os"
	"testing"
)

// TestMain stubs the executability gate to always resolve, so the behavioral
// tests in this package are deterministic regardless of which binaries the test
// host happens to have on PATH. Tests that exercise the gate set lookPath
// themselves and restore it.
func TestMain(m *testing.M) {
	lookPath = func(string) bool { return true }
	os.Exit(m.Run())
}

func TestLineSkipsUnresolvableCommands(t *testing.T) {
	// Resolve everything except two shell functions, the way a real shell would:
	// a function defined in the caller's shell is not on PATH.
	funcs := map[string]bool{"stamp_first": true, "load-nvmrc": true}
	prev := lookPath
	lookPath = func(name string) bool { return !funcs[name] }
	defer func() { lookPath = prev }()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"bare shell function is not wrapped",
			"load-nvmrc",
			"load-nvmrc",
		},
		{
			"shell function as final pipeline stage is not wrapped",
			"cat out | stamp_first",
			"cat out | stamp_first",
		},
		{
			"function with env prefix is not wrapped",
			"FOO=bar load-nvmrc",
			"FOO=bar load-nvmrc",
		},
		{
			"real binary is still wrapped",
			"git status",
			"ctx-wire run git status",
		},
		{
			"mixed compound: real wrapped, function left alone",
			"git status && stamp_first",
			"ctx-wire run git status && stamp_first",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := Line(tt.in); got != tt.want {
				t.Errorf("Line(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Explain must stay a pure shape classifier: the runtime executability gate
// lives in Line, so Explain still reports an unresolved command as wrappable
// (discover analyzes history from other hosts and must not depend on the local
// PATH).
func TestExplainIgnoresExecutabilityGate(t *testing.T) {
	prev := lookPath
	lookPath = func(string) bool { return false }
	defer func() { lookPath = prev }()

	if got := Line("totally_made_up_cmd --x"); got != "totally_made_up_cmd --x" {
		t.Errorf("Line should not wrap an unresolved command, got %q", got)
	}
	if r := Explain("totally_made_up_cmd --x"); len(r.Segments) != 1 || !r.Segments[0].Wrapped {
		t.Errorf("Explain should classify by shape (wrapped), got %+v", r.Segments)
	}
}
