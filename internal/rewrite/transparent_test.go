package rewrite

import (
	"testing"

	"ctx-wire/internal/commandpolicy"
)

func TestTransparentPrefixRewrite(t *testing.T) {
	commandpolicy.SetTransparentPrefixes([]string{"docker exec web"})
	t.Cleanup(func() { commandpolicy.SetTransparentPrefixes(nil) })

	// The inner command is wrapped; the wrapper prefix is preserved.
	if got, want := Line("docker exec web git status"), "docker exec web ctx-wire run git status"; got != want {
		t.Errorf("Line()\n got  %q\n want %q", got, want)
	}
	// Without a configured prefix, the whole thing is just a docker command.
	commandpolicy.SetTransparentPrefixes(nil)
	if got := Line("docker exec web git status"); got != "ctx-wire run docker exec web git status" {
		t.Errorf("unconfigured prefix should wrap docker itself, got %q", got)
	}
}
