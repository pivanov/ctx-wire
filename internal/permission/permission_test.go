package permission

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchBash(t *testing.T) {
	cases := []struct {
		pattern, command string
		want             bool
	}{
		{"git push:*", "git push", true},
		{"git push:*", "git push origin main", true},
		{"git push:*", "git pushup", false}, // prefix must be a whole token
		{"rm -rf:*", "rm -rf /tmp/x", true},
		{"npm test", "npm test", true},
		{"npm test", "npm test --watch", false}, // exact, no wildcard
		{"curl *", "curl https://x", true},
		{"*", "anything goes", true},
		{"docker *push", "docker image push", true},
		{"docker *push", "docker image pull", false},
		// Prefix and suffix must not overlap in a short string.
		{"aa*aa", "aaa", false},
		{"aa*aa", "aaaa", true},
		{"git*push", "gitpush", true},
	}
	for _, c := range cases {
		if got := matchBash(c.pattern, c.command); got != c.want {
			t.Errorf("matchBash(%q, %q) = %v, want %v", c.pattern, c.command, got, c.want)
		}
	}
}

func TestDecidePrecedenceAndDefault(t *testing.T) {
	r := Rules{
		deny: []string{"rm -rf:*", "git push:*"},
		ask:  []string{"curl:*"},
	}
	if r.Decide("rm -rf /") != Deny {
		t.Error("rm -rf should be denied")
	}
	if r.Decide("curl https://x") != Ask {
		t.Error("curl should ask")
	}
	if r.Decide("go test ./...") != Allow {
		t.Error("unmatched command should default to allow")
	}
	// Compound: deny on any segment wins.
	if r.Decide("cd /tmp && rm -rf x") != Deny {
		t.Error("deny on a compound segment should win")
	}
	if r.Decide("ls | curl example.com") != Ask {
		t.Error("ask on a pipe segment should be honored")
	}
}

func TestDecideNoRulesIsAllow(t *testing.T) {
	if (Rules{}).Decide("rm -rf /") != Allow {
		t.Fatal("no rules must default to Allow (fail-open)")
	}
}

func TestDenyDoesNotMatchInsideQuotedArg(t *testing.T) {
	r := Rules{deny: []string{"rm -rf:*"}}
	// The rm is inside a quoted echo argument, not an executed segment.
	if got := r.Decide(`echo "rm -rf x"`); got != Allow {
		t.Fatalf("quoted rm must not trigger deny, got %v", got)
	}
}

func TestEscapedQuoteDoesNotEndSegment(t *testing.T) {
	r := Rules{deny: []string{"rm -rf:*"}}
	// The escaped quote inside the double-quoted string must not terminate the
	// quote early and expose the `rm -rf` as a separate executed segment.
	if got := r.Decide(`echo "a\" rm -rf x"`); got != Allow {
		t.Fatalf("escaped quote should keep rm inside the quoted arg, got %v", got)
	}
}

func TestSplitSegmentsEscapedQuote(t *testing.T) {
	// A \" inside double quotes is part of the single segment, not a delimiter.
	segs := splitSegments(`echo "a\";b" && ls`)
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d: %v", len(segs), segs)
	}
	if segs[0] != `echo "a\";b"` {
		t.Errorf("first segment mangled: %q", segs[0])
	}
}

func TestBashPatternsParsing(t *testing.T) {
	pats := bashPatterns([]string{"Bash(git push:*)", "Bash", "Read(/etc/*)", "Bash(rm -rf:*)", "WebFetch"})
	want := map[string]bool{"git push:*": true, "*": true, "rm -rf:*": true}
	if len(pats) != 3 {
		t.Fatalf("expected 3 Bash patterns, got %v", pats)
	}
	for _, p := range pats {
		if !want[p] {
			t.Errorf("unexpected pattern %q", p)
		}
	}
}

func TestLoadMergesSettingsFiles(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "settings.json"), `{"permissions":{"deny":["Bash(rm -rf:*)"],"ask":[],"allow":[]}}`)
	writeJSON(t, filepath.Join(dir, "settings.local.json"), `{"permissions":{"deny":["Bash(git push:*)"],"ask":["Bash(curl:*)"]}}`)
	r := Load(dir)
	if r.Decide("rm -rf /") != Deny || r.Decide("git push origin") != Deny {
		t.Error("merged deny rules from both files should apply")
	}
	if r.Decide("curl x") != Ask {
		t.Error("ask rule from local settings should apply")
	}
	if r.Decide("go build ./...") != Allow {
		t.Error("unmatched should be allow")
	}
}

func TestLoadMissingDirIsEmpty(t *testing.T) {
	r := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if r.Decide("rm -rf /") != Allow {
		t.Fatal("missing settings must fail open to Allow")
	}
}

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
