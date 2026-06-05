package tune

import (
	"strings"
	"testing"
)

func TestSanitizeHomeRedaction(t *testing.T) {
	san := NewSanitizer("/Users/alice", "")
	if got := san.Sample("cat /Users/alice/notes.txt"); got != "cat $HOME/notes.txt" {
		t.Fatalf("home redaction: got %q", got)
	}
}

func TestSanitizeProjectRedaction(t *testing.T) {
	san := NewSanitizer("/Users/alice", "/Users/alice/work/repo")
	if got := san.Sample("cat /Users/alice/work/repo/src/app.ts"); got != "cat $PROJECT/src/app.ts" {
		t.Fatalf("project redaction: got %q", got)
	}
}

func TestSanitizeProjectPreferredOverHome(t *testing.T) {
	// A project nested under home must redact to $PROJECT, not $HOME/...; a sibling
	// path under home still redacts to $HOME.
	san := NewSanitizer("/Users/alice", "/Users/alice/work/repo")
	got := san.Sample("ls /Users/alice/work/repo /Users/alice/other")
	if !strings.Contains(got, "$PROJECT") || !strings.Contains(got, "$HOME/other") {
		t.Fatalf("mixed redaction: got %q", got)
	}
	if strings.Contains(got, "/Users/alice") {
		t.Fatalf("raw home leaked: %q", got)
	}
}

func TestSanitizeRootRedactionRequiresPathBoundary(t *testing.T) {
	san := NewSanitizer("/Users/alice", "/Users/alice/work/repo")
	got := san.Sample("ls /Users/alice/work/repo /Users/alice/work/repository /Users/alicebob")
	if !strings.Contains(got, "$PROJECT") {
		t.Fatalf("project root should be redacted: %q", got)
	}
	if !strings.Contains(got, "$HOME/work/repository") {
		t.Fatalf("project root should not redact repository prefix: %q", got)
	}
	if !strings.Contains(got, "/Users/alicebob") {
		t.Fatalf("home root should not redact alicebob prefix: %q", got)
	}
	if strings.Contains(got, "$PROJECTsitory") || strings.Contains(got, "$HOMEbob") {
		t.Fatalf("boundary redaction mangled adjacent path: %q", got)
	}
}

func TestSanitizeLongPathCompaction(t *testing.T) {
	san := NewSanitizer("", "")
	if got := san.Sample("cat /usr/local/share/very/deep/path/file.ts"); got != "cat /usr/.../path/file.ts" {
		t.Fatalf("path compaction: got %q", got)
	}
}

func TestSanitizeShortPathUnchanged(t *testing.T) {
	san := NewSanitizer("", "")
	if got := san.Sample("cat /etc/hosts"); got != "cat /etc/hosts" {
		t.Fatalf("short path should be unchanged: got %q", got)
	}
}

func TestSanitizeMarkerRootedPathCompaction(t *testing.T) {
	san := NewSanitizer("/Users/alice", "")
	got := san.Sample("cat /Users/alice/a/b/c/d/e/f.ts")
	if got != "cat $HOME/.../e/f.ts" {
		t.Fatalf("marker-rooted compaction: got %q", got)
	}
}

func TestSanitizeSecretRedaction(t *testing.T) {
	san := NewSanitizer("", "")
	got := san.Sample("deploy --token sk-ant-SECRETVALUE0123456789abcdef")
	if strings.Contains(got, "SECRETVALUE") {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected [REDACTED]: %q", got)
	}
}

func TestSanitizeLengthCap(t *testing.T) {
	san := NewSanitizer("", "")
	san.MaxLen = 20
	got := san.Sample("echo " + strings.Repeat("x", 100))
	if len([]rune(got)) != 20 {
		t.Fatalf("length cap: got len %d (%q)", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("capped output should end with ...: %q", got)
	}
}

func TestSanitizeRootSlashIgnored(t *testing.T) {
	// Roots of "/" must never blank out a command.
	san := NewSanitizer("/", "/")
	if got := san.Sample("cat /etc/hosts"); got != "cat /etc/hosts" {
		t.Fatalf("root '/' should be ignored: got %q", got)
	}
}

func TestSanitizeDoesNotMangleURL(t *testing.T) {
	san := NewSanitizer("", "")
	in := "curl https://host.example.com/a/b/c/d/e/f/g"
	if got := san.Sample(in); !strings.Contains(got, "https://host.example.com/a/b/c/d/e/f/g") {
		t.Fatalf("URL path should not be compacted: got %q", got)
	}
}
