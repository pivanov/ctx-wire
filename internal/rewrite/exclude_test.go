package rewrite

import (
	"testing"

	"ctx-wire/internal/commandpolicy"
)

func TestExcludedCommandNotWrapped(t *testing.T) {
	commandpolicy.SetExcludedCommands([]string{"curl"})
	t.Cleanup(func() { commandpolicy.SetExcludedCommands(nil) })

	if got := Line("curl https://example.test"); got != "curl https://example.test" {
		t.Errorf("excluded command must not be wrapped, got %q", got)
	}
	// A non-excluded command on the same machinery still wraps.
	if got := Line("git status"); got != "ctx-wire run git status" {
		t.Errorf("non-excluded command should wrap, got %q", got)
	}
	// Explain surfaces the reason (it shares passReason).
	r := Explain("curl https://example.test")
	if len(r.Segments) != 1 || r.Segments[0].Wrapped {
		t.Fatalf("expected unwrapped segment, got %+v", r.Segments)
	}
}
