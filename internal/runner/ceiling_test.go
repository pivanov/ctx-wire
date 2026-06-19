package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"ctx-wire/internal/tee"
)

// The passthrough-ceiling contract: unfiltered output at or under the ceiling
// is byte-exact; over it, the head streams live, the tail survives, the middle
// is omitted with a marker, and the full scrubbed output is KEPT in the spool
// (the recovery rule). CTX_WIRE_TRUNCATE=none disables the ceiling entirely.

// shrinkCeiling makes the ceiling small enough to exercise without megabytes.
func shrinkCeiling(t *testing.T, head, tail int) {
	t.Helper()
	oldH, oldT := passthroughHeadBytes, passthroughTailBytes
	passthroughHeadBytes, passthroughTailBytes = head, tail
	t.Cleanup(func() { passthroughHeadBytes, passthroughTailBytes = oldH, oldT })
}

func runStream(t *testing.T, script string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	var out, errBuf bytes.Buffer
	spool := tee.NewSpool("ceiling-test")
	code, err := streamLive(context.Background(), "sh", []string{"-c", script}, "sh -c test", spool, &out, &errBuf)
	if err != nil {
		t.Fatalf("streamLive: %v", err)
	}
	return out.String(), errBuf.String(), code
}

func TestCeilingUnderLimitIsByteExact(t *testing.T) {
	shrinkCeiling(t, 4096, 1024)
	stdout, stderr, code := runStream(t, `seq 1 100`)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := ""
	for i := 1; i <= 100; i++ {
		want += fmt.Sprintf("%d\n", i)
	}
	if stdout != want {
		t.Errorf("under-ceiling output not byte-exact:\n got %d bytes\nwant %d bytes", len(stdout), len(want))
	}
	if strings.Contains(stdout, "ctx-wire:") || strings.Contains(stderr, "[full output:") {
		t.Errorf("under-ceiling run must have no marker and no kept spool:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
}

func TestCeilingOverLimitKeepsHeadTailMarkerAndSpool(t *testing.T) {
	shrinkCeiling(t, 2048, 512)
	// Numbered lines so head/tail content is checkable: ~7 bytes per line,
	// 3000 lines is ~20 KB, far over head+tail.
	stdout, stderr, code := runStream(t, `seq 1 3000`)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(stdout, "1\n2\n3\n") {
		t.Errorf("head must stream byte-exact from the start, got %q", stdout[:24])
	}
	if !strings.Contains(stdout, "bytes omitted (over the 2560-byte passthrough ceiling)") {
		t.Errorf("omission marker missing or wrong:\n%q", stdout)
	}
	if !strings.HasSuffix(stdout, "3000\n") {
		t.Errorf("tail must end with the final line, got %q", stdout[len(stdout)-24:])
	}
	if len(stdout) > 2048+512+256 {
		t.Errorf("emitted %d bytes, want roughly head+tail+marker", len(stdout))
	}
	// The recovery rule: the spool is KEPT on a successful-but-truncated run.
	if !strings.Contains(stderr, "[full output:") {
		t.Errorf("expected the kept-spool hint on stderr, got %q", stderr)
	}
	// Extract the hash from the hint: "[full output: ctx-wire fetch <hash>]"
	const fetchPrefix = "[full output: ctx-wire fetch "
	hint := strings.TrimSpace(stderr)
	if !strings.HasPrefix(hint, fetchPrefix) {
		t.Fatalf("hint not in expected form, got %q", hint)
	}
	hashVal := strings.TrimSuffix(strings.TrimPrefix(hint, fetchPrefix), "]")
	path, ok := tee.Resolve(hashVal)
	if !ok {
		t.Fatalf("tee.Resolve(%q): not found", hashVal)
	}
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spool not readable at %q: %v", path, err)
	}
	if !strings.Contains(string(disk), "\n1500\n") {
		t.Errorf("spool must hold the omitted middle (line 1500)")
	}
}

func TestCeilingNoneDisables(t *testing.T) {
	shrinkCeiling(t, 1024, 256)
	t.Setenv("CTX_WIRE_TRUNCATE", "none")
	stdout, _, code := runStream(t, `seq 1 3000`)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stdout, "ctx-wire:") {
		t.Errorf("CTX_WIRE_TRUNCATE=none must disable the ceiling:\n%q", stdout[:200])
	}
	if !strings.HasSuffix(stdout, "3000\n") || !strings.HasPrefix(stdout, "1\n") {
		t.Error("with the ceiling off, output must be complete")
	}
}

func TestCeilingFailureKeepsFullSpoolAndTail(t *testing.T) {
	shrinkCeiling(t, 2048, 512)
	stdout, stderr, code := runStream(t, `seq 1 3000; echo "FATAL: the actual error" >&2; exit 7`)
	if code != 7 {
		t.Fatalf("exit %d, want 7", code)
	}
	// stderr is small: it must arrive intact (the shared head budget may be
	// spent by stdout, but the tail ring preserves it).
	if !strings.Contains(stderr, "FATAL: the actual error") {
		t.Errorf("the failure signal was lost:\n%q", stderr)
	}
	if !strings.HasSuffix(stdout, "3000\n") {
		t.Error("failed run must still keep the stdout tail")
	}
	if !strings.Contains(stderr, "[full output:") {
		t.Error("failed run must keep the spool")
	}
}

func TestCeilingBothStreamsShareHeadBudget(t *testing.T) {
	shrinkCeiling(t, 1024, 256)
	// stdout spends the whole head; stderr lands after it and must still
	// surface through its own tail ring.
	stdout, stderr, code := runStream(t, `seq 1 2000; echo "stderr-after-budget" >&2`)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr, "stderr-after-budget") {
		t.Errorf("stderr tail lost after stdout spent the head budget:\n%q", stderr)
	}
	total := len(stdout) + len(stderr)
	if total > 1024+2*256+512 {
		t.Errorf("combined emit %d bytes, want bounded by head + both tails + markers", total)
	}
}

// TestCeilingSnapsToRuneBoundary pins the CJK fix: when the ceiling fires on
// multibyte UTF-8 (CJK, emoji), neither the head cut nor the tail front-trim
// may land inside a rune, or the marker would corrupt a character into mojibake
// (invalid UTF-8). 中 is 3 bytes; head=10 and tail=10 are not multiples of 3, so
// both cuts deliberately land mid-rune, and the output must still be valid UTF-8
// of whole 中s.
func TestCeilingSnapsToRuneBoundary(t *testing.T) {
	shrinkCeiling(t, 10, 10)
	// 100 中 = 300 bytes, far over head+tail, so the ceiling fires and both cuts
	// fall mid-rune. Octal escapes (\344\270\255 = 中) keep it POSIX-portable.
	stdout, _, code := runStream(t, `printf '\344\270\255%.0s' $(seq 1 100)`)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "bytes omitted") {
		t.Fatalf("ceiling did not fire:\n%q", stdout)
	}
	if !utf8.ValidString(stdout) {
		t.Errorf("output is not valid UTF-8 (a rune was split at a cut):\n%q", stdout)
	}
	head, _, found := strings.Cut(stdout, "\n[ctx-wire:")
	if !found {
		t.Fatalf("marker not found:\n%q", stdout)
	}
	if strings.TrimRight(head, "中") != "" {
		t.Errorf("head is not whole 中 runes (split at the head cut): %q", head)
	}
	_, tail, _ := strings.Cut(stdout, "spooled]\n")
	if strings.TrimRight(tail, "中") != "" {
		t.Errorf("tail is not whole 中 runes (split at the tail cut): %q", tail)
	}
}

// TestCeilingHeadZeroOrderAcrossWrites pins the load-bearing `w.c.head = 0`
// invariant: after the head cut snaps down (diverting a partial rune to the
// tail), the budget must read as spent, or a LATER write would race bytes into
// the head ahead of the already-diverted rune and reorder the output. The bug
// only shows across writes and only with DISTINCT runes (identical runes can't
// reveal a reorder), so this is the test the single-Write snap test could not
// be. Delete `w.c.head = 0` and this fails (本/日 swap); the suite otherwise
// stays green.
func TestCeilingHeadZeroOrderAcrossWrites(t *testing.T) {
	var dst bytes.Buffer
	w := newStreamCeiling(10, 1000).writer(&dst) // head=10 splits the 4th 3-byte rune
	w.Write([]byte("中文字本"))                      // 12 bytes; head snaps to 9 (中文字), 本 diverts to tail
	w.Write([]byte("日"))                         // must land AFTER 本 in the tail, not race into the head
	if _, err := w.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := dst.String(); got != "中文字本日" {
		t.Fatalf("out of order across writes: got %q want 中文字本日", got)
	}
}

func TestSnapDownRune(t *testing.T) {
	zh := []byte("中") // e4 b8 ad
	cases := []struct {
		name string
		p    []byte
		n    int
		want int
	}{
		{"clean boundary", zh, 3, 3},
		{"mid rune", zh, 2, 0},
		{"lead byte only (continuations not yet in chunk)", zh[:1], 1, 0},
		{"second rune lead only", []byte("中中")[:4], 4, 3},
		{"ascii untouched", []byte("abcd"), 3, 3},
		{"invalid binary kept", []byte{0xff}, 1, 1},
	}
	for _, c := range cases {
		if got := snapDownRune(c.p, c.n); got != c.want {
			t.Errorf("%s: snapDownRune(%v,%d)=%d want %d", c.name, c.p, c.n, got, c.want)
		}
	}
}

func TestSnapUpRune(t *testing.T) {
	zh := []byte("中")
	if got := snapUpRune(zh, 1); got != 3 { // drop the partial leading 中
		t.Errorf("snapUpRune(中,1)=%d want 3", got)
	}
	if got := snapUpRune([]byte("a中"), 1); got != 1 { // already a boundary (中's lead)
		t.Errorf("snapUpRune(a中,1)=%d want 1", got)
	}
	// Invalid binary: a long continuation-byte run must not be skipped past one
	// rune's worth (cap at UTFMax-1 = 3).
	bin := []byte{0x80, 0x80, 0x80, 0x80, 0x80}
	if got := snapUpRune(bin, 0); got != 3 {
		t.Errorf("snapUpRune(binary,0)=%d want 3 (capped)", got)
	}
}
