package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/tee"
)

// spoolPayload writes payload into a new Spool (with the given dir as
// CTX_WIRE_TEE_DIR), finalizes it, and returns the resulting path and hash.
func spoolPayload(t *testing.T, dir, slug, payload string) (path, hash string) {
	t.Helper()
	s := tee.NewSpool(slug)
	if _, err := s.Write([]byte(payload)); err != nil {
		t.Fatalf("Spool.Write: %v", err)
	}
	p, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file to be kept")
	}
	// Extract hash from the path.
	base := strings.TrimSuffix(filepath.Base(p), ".log")
	i := strings.LastIndex(base, "_")
	if i < 0 {
		t.Fatalf("spool path %q has no embedded hash", p)
	}
	return p, base[i+1:]
}

func TestCmdFetchHelpExitsZero(t *testing.T) {
	if code := cmdFetch([]string{"--help"}); code != 0 {
		t.Fatalf("cmdFetch --help exit = %d, want 0", code)
	}
}

func TestCmdFetchNoArgsExitsZero(t *testing.T) {
	if code := cmdFetch(nil); code != 0 {
		t.Fatalf("cmdFetch nil exit = %d, want 0", code)
	}
}

func TestCmdFetchNoArgsEmptySliceExitsZero(t *testing.T) {
	if code := cmdFetch([]string{}); code != 0 {
		t.Fatalf("cmdFetch [] exit = %d, want 0", code)
	}
}

func TestCmdFetchInvalidHandleExitsTwo(t *testing.T) {
	// CTX_WIRE_TEE_DIR must be set so Resolve doesn't fail for unrelated reasons.
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	cases := []string{
		"zz",       // not hex and too short
		"abc",      // too short (< minFetchPrefix=8)
		"xyzxyzxy", // not hex (x, y, z are not hex digits)
		"gggggggg", // not hex ('g' is not a hex digit)
	}
	for _, c := range cases {
		if code := cmdFetch([]string{c}); code != 2 {
			t.Errorf("cmdFetch(%q) exit = %d, want 2", c, code)
		}
	}
}

func TestCmdFetchUnknownHashExitsOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)
	// "deadbeefdeadbeef" is 16 lowercase hex chars, valid prefix but no matching file.
	if code := cmdFetch([]string{"deadbeefdeadbeef"}); code != 1 {
		t.Fatalf("cmdFetch unknown hash exit = %d, want 1", code)
	}
}

func TestCmdFetchHappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)

	payload := strings.Repeat("fetch content line\n", 50)
	_, hash := spoolPayload(t, dir, "fetch-test", payload)

	// Discard stdout so the recovered payload does not leak into test output;
	// TestCmdFetchWritesSpoolToStdout covers the streamed content separately.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer devnull.Close()
	origStdout := os.Stdout
	os.Stdout = devnull
	code := cmdFetch([]string{hash[:12]})
	os.Stdout = origStdout

	// The happy path: cmdFetch must exit 0 when the hash is found.
	if code != 0 {
		t.Fatalf("cmdFetch happy path exit = %d, want 0", code)
	}
}

func TestCmdFetchIsHexPrefixHelper(t *testing.T) {
	// isHexPrefix is called on the already-lowercased input, so only lowercase
	// hex is valid here; uppercase would have been lowercased before this call.
	cases := []struct {
		s    string
		want bool
	}{
		{"deadbeef", true},
		{"0123456789abcdef", true},
		{"xyz", false},
		{"", false},
		{"deadbeeg", false}, // 'g' is not hex
		{"DEADBEEF", false}, // uppercase: not accepted (lowercase only)
	}
	for _, c := range cases {
		if got := isHexPrefix(c.s); got != c.want {
			t.Errorf("isHexPrefix(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

// TestCmdFetchWritesSpoolToStdout verifies that cmdFetch streams the spool content
// to stdout. We use a pipe to capture stdout during the call.
func TestCmdFetchWritesSpoolToStdout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)

	payload := strings.Repeat(fmt.Sprintf("line-%d\n", 42), 50)
	_, hash := spoolPayload(t, dir, "stdout-test", payload)

	// Redirect stdout to a pipe so we can capture cmdFetch's output.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	code := cmdFetch([]string{hash[:12]})

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	r.Close()

	if code != 0 {
		t.Fatalf("cmdFetch exit = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), fmt.Sprintf("line-%d", 42)) {
		t.Errorf("stdout does not contain expected spool content: %q", buf.String())
	}
}
