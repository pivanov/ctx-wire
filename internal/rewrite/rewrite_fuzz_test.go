package rewrite

import (
	"strings"
	"testing"
)

func FuzzLine(f *testing.F) {
	for _, seed := range []string{
		"git status",
		"FOO=bar git status && npm test",
		`echo "a && b" | grep a`,
		"cat source > dest",
		"for f in a b; do echo $f; done",
		"(git status)",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		got := Line(line)
		if strings.Contains(got, prefix+prefix) {
			t.Fatalf("double wrapped command: %q", got)
		}
		again := Line(got)
		if again != got {
			t.Fatalf("Line is not idempotent: %q -> %q", got, again)
		}
	})
}
