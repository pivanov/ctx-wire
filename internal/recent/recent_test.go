package recent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// TestConcurrentRecordNoLostWrites pins the cross-process safety of the
// read-modify-rewrite: parallel records must not clobber each other (the bug the
// lock fixes after moving off atomic O_APPEND).
func TestConcurrentRecordNoLostWrites(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	opts := Options{Enabled: true, MaxEntries: 100}
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			Record(opts, Entry{Command: fmt.Sprintf("c%d", k), Emitted: "x"})
		}(i)
	}
	wg.Wait()
	if got := List(); len(got) != n {
		t.Errorf("concurrent records kept %d entries, want %d (a lost write means the lock failed)", len(got), n)
	}
}

func TestMaxEntriesEnforced(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	opts := Options{Enabled: true, MaxEntries: 3}
	for i := 0; i < 10; i++ {
		Record(opts, Entry{Command: fmt.Sprintf("cmd%d", i), Emitted: "x"})
	}
	got := List()
	if len(got) != 3 {
		t.Fatalf("kept %d entries, want 3 (MaxEntries must be honored regardless of file size)", len(got))
	}
	if got[0].Command != "cmd7" || got[2].Command != "cmd9" {
		t.Errorf("kept the wrong window: %s..%s, want cmd7..cmd9", got[0].Command, got[2].Command)
	}
}

func TestLargeEntryRoundTrips(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// Quotes and newlines inflate heavily under JSON escaping; the entry must
	// still read back (a fixed scanner buffer would have dropped it).
	body := strings.Repeat("\"x\"\n", perBodyCap/4)
	Record(Options{Enabled: true, RawBodies: true}, Entry{Command: "big", Emitted: body, Raw: body})
	if got := List(); len(got) != 1 {
		t.Fatalf("large entry was dropped: got %d entries, want 1", len(got))
	}
}

func TestStoreFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	Record(Options{Enabled: true, MaxEntries: 1}, Entry{Command: "a", Emitted: "x"})
	Record(Options{Enabled: true, MaxEntries: 1}, Entry{Command: "b", Emitted: "y"}) // forces a rewrite
	info, err := os.Stat(filepath.Join(dir, "ctx-wire", "recent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("store file perm = %o, want 600 (private; it may hold raw bodies)", perm)
	}
}

func TestApplyEnvDisables(t *testing.T) {
	base := Options{Enabled: true, RawBodies: true}
	t.Setenv("CTX_WIRE_RETENTION", "0")
	if ApplyEnv(base).Enabled {
		t.Error("CTX_WIRE_RETENTION=0 should disable retention")
	}
	t.Setenv("CTX_WIRE_RETENTION", "")
	t.Setenv("CTX_WIRE_RETENTION_RAW", "off")
	if got := ApplyEnv(base); !got.Enabled || got.RawBodies {
		t.Errorf("CTX_WIRE_RETENTION_RAW=off should drop only the raw tier: %+v", got)
	}
}

func TestRecordDisabledIsNoop(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	Record(Options{Enabled: false}, Entry{Command: "x", Emitted: "y", Raw: "z"})
	if got := List(); len(got) != 0 {
		t.Errorf("disabled retention stored %d entries, want 0", len(got))
	}
}

func TestRecordTiers(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// Lean tier: a raw body must NOT be persisted even if passed.
	Record(Options{Enabled: true, RawBodies: false}, Entry{Command: "cmd1", Emitted: "out1", Raw: "should-not-persist"})
	// Raw tier: the raw body is persisted.
	Record(Options{Enabled: true, RawBodies: true}, Entry{Command: "cmd2", Emitted: "out2", Raw: "rawbody2"})

	got := List()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Raw != "" {
		t.Errorf("lean tier must not store a raw body, got %q", got[0].Raw)
	}
	if got[0].Hash == "" {
		t.Error("entry should carry a hash of the emitted output")
	}
	if got[1].Raw != "rawbody2" {
		t.Errorf("raw tier should store the raw body, got %q", got[1].Raw)
	}
}

func TestClipBoundsBody(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	big := strings.Repeat("a", perBodyCap+1000)
	Record(Options{Enabled: true, RawBodies: true}, Entry{Command: "c", Emitted: big, Raw: big})
	got := List()
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if len(got[0].Emitted) != perBodyCap || len(got[0].Raw) != perBodyCap {
		t.Errorf("bodies should be clipped to perBodyCap, got emit=%d raw=%d", len(got[0].Emitted), len(got[0].Raw))
	}
}

// writeLockFile creates a lock file in dir with the given PID content and
// returns its path.
func writeLockFile(t *testing.T, dir string, pid int) string {
	t.Helper()
	p := filepath.Join(dir, "store.lock")
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)+"\n"), 0600); err != nil {
		t.Fatalf("writeLockFile: %v", err)
	}
	return p
}

// TestStaleLockDeadPIDWithinTTL: a lock holding a dead PID should be reclaimed
// even when its mtime is fresh.
func TestStaleLockDeadPIDWithinTTL(t *testing.T) {
	dir := t.TempDir()
	// PID 999999999 is virtually guaranteed to be dead.
	p := writeLockFile(t, dir, 999999999)
	if !staleLock(p) {
		t.Error("staleLock returned false for a dead PID within TTL; want true")
	}
}

// TestStaleLockLivePIDWithinTTL: a lock holding the current process's PID with
// a fresh mtime must NOT be reclaimed.
func TestStaleLockLivePIDWithinTTL(t *testing.T) {
	dir := t.TempDir()
	p := writeLockFile(t, dir, os.Getpid())
	if staleLock(p) {
		t.Error("staleLock returned true for a live PID within TTL; want false")
	}
}

// TestStaleLockOldMtime: any lock whose mtime exceeds lockTTL must be reclaimed
// regardless of what PID it contains.
func TestStaleLockOldMtime(t *testing.T) {
	dir := t.TempDir()
	// Use the live PID so the PID check alone would say "not stale", proving
	// the age check fires independently.
	p := writeLockFile(t, dir, os.Getpid())
	old := time.Now().Add(-(lockTTL + time.Second))
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if !staleLock(p) {
		t.Error("staleLock returned false for an old lock; want true")
	}
}

func TestClipCutsOnRuneBoundary(t *testing.T) {
	// Place a 3-byte rune so its 2nd/3rd bytes sit at/after the cap.
	s := strings.Repeat("a", perBodyCap-1) + "世" + strings.Repeat("b", 16)
	out := clip(s)
	if !utf8.ValidString(out) {
		t.Fatalf("clip produced invalid UTF-8 (split rune at the cap)")
	}
	if len(out) > perBodyCap {
		t.Fatalf("clip exceeded cap: %d > %d", len(out), perBodyCap)
	}
	if !strings.HasPrefix(s, out) {
		t.Fatalf("clip result is not a byte-prefix of the input")
	}
	if got := clip("hello"); got != "hello" {
		t.Fatalf("clip(short) = %q, want unchanged", got)
	}
}
