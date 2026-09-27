package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/tee"
)

// A filter that passes structured output through untouched (go-list-json) must
// stay byte-exact over the byte ceiling when the reader may be a program: a
// manual `ctx-wire run go list -json ./... | jq` broke when the backstop cut
// the stream mid-object. Only a known agent reader (hook) gets the cut.
func TestBackstopKeepsUntouchedJSONStreamWhole(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	shrinkCeiling(t, 64, 32)
	prev := stdoutIsFile
	stdoutIsFile = func() bool { return false }
	t.Cleanup(func() { stdoutIsFile = prev })
	reg := mustRegistry(t)

	var stream strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&stream, "{\n  \"ImportPath\": \"example.com/m/pkg%d\",\n  \"Name\": \"pkg%d\"\n}\n", i, i)
	}
	file := filepath.Join(t.TempDir(), "golist.json")
	if err := os.WriteFile(file, []byte(stream.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		t.Helper()
		out, _, _, _, err := runBuffered(context.Background(), reg, reg.Find("go list -json ./..."), "cat",
			[]string{file}, "go list -json ./...", "go list -json ./...", tee.NewSpool("golist"))
		if err != nil {
			t.Fatalf("runBuffered: %v", err)
		}
		return out
	}

	t.Setenv(EnvSource, "")
	out := run()
	if out != stream.String() {
		t.Fatalf("manual run altered a structured stream:\n%q", out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var v map[string]any
		if err := dec.Decode(&v); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("stream no longer parses: %v", err)
		}
	}

	t.Setenv(EnvSource, "hook")
	if out := run(); !strings.Contains(out, "bytes omitted") {
		t.Fatalf("hook reader should get the ceiling, got %d bytes", len(out))
	}
}

// Scrubbing folds a multi-line secret into one spool line, shifting every later
// line number, so a ranged `fetch --lines` pointer would skip real lines. The
// hint must fall back to the whole spool whenever the line counts disagree.
func TestRangeHintFallsBackWhenScrubFoldsLines(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)

	var body strings.Builder
	body.WriteString("before the key\n-----BEGIN RSA PRIVATE KEY-----\n")
	for i := 0; i < 25; i++ {
		body.WriteString("MIIBVgIBADANBgkqhkiG9w0BAQEFAASCAUAwggE8FAKEKEYMATERIAL\n")
	}
	body.WriteString("-----END RSA PRIVATE KEY-----\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&body, "line-%d\n", i)
	}
	file := filepath.Join(t.TempDir(), "withkey.txt")
	if err := os.WriteFile(file, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, hint, _, err := runBuffered(context.Background(), reg, reg.Find("cat "+file), "cat",
		[]string{file}, "cat "+file, "cat withkey.txt", tee.NewSpool("withkey"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if strings.Contains(hint, "--lines") {
		t.Fatalf("ranged hint over a scrub-shifted spool would skip lines: %q", hint)
	}
	if !strings.Contains(hint, "[full output: ctx-wire fetch ") {
		t.Fatalf("expected the whole-spool hint, got %q", hint)
	}
}
