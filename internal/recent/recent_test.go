package recent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
