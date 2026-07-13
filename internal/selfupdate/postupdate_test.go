package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaybeRunPostUpdateRunsOncePerVersion(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	calls := 0
	migrate := func() bool { calls++; return true }

	// First sight of 1.2.3 (no marker yet): migrate runs.
	MaybeRunPostUpdate("1.2.3", migrate)
	if calls != 1 {
		t.Fatalf("first run: calls=%d, want 1", calls)
	}
	// Same version again: marker matches, migrate does not re-run.
	MaybeRunPostUpdate("1.2.3", migrate)
	if calls != 1 {
		t.Fatalf("unchanged version: calls=%d, want still 1", calls)
	}
	// A newer version: migrate runs again.
	MaybeRunPostUpdate("1.2.4", migrate)
	if calls != 2 {
		t.Fatalf("after version bump: calls=%d, want 2", calls)
	}
}

func TestMaybeRunPostUpdateSkipsDevBuild(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	calls := 0
	MaybeRunPostUpdate("dev", func() bool { calls++; return true })
	if calls != 0 {
		t.Fatalf("dev/unversioned build must not run migrations, calls=%d", calls)
	}
	if v := readSyncedVersion(); v != "" {
		t.Fatalf("dev build wrote a marker %q", v)
	}
}

func TestMaybeRunPostUpdateWritesMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	MaybeRunPostUpdate("2.0.0", func() bool { return true })
	if got := readSyncedVersion(); got != "2.0.0" {
		t.Fatalf("synced-version = %q, want 2.0.0", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "ctx-wire", "synced-version")); err != nil {
		t.Fatalf("marker file not written: %v", err)
	}
}

func TestMaybeRunPostUpdateRetriesWhenUnsettled(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	calls := 0
	// migrate reports "not settled": it must not record the version, so it runs
	// again on the next call for the same version.
	migrate := func() bool { calls++; return false }
	MaybeRunPostUpdate("3.0.0", migrate)
	MaybeRunPostUpdate("3.0.0", migrate)
	if calls != 2 {
		t.Fatalf("unsettled migrate should retry: calls=%d, want 2", calls)
	}
	if v := readSyncedVersion(); v != "" {
		t.Fatalf("unsettled migrate must not record a marker, got %q", v)
	}
}

func TestMaybeRunPostUpdateRecoversPanic(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// A panicking migration must not escape (it would abort the user's command),
	// and must not record the version (a panic is not a settled outcome).
	MaybeRunPostUpdate("4.0.0", func() bool { panic("boom") })
	if v := readSyncedVersion(); v != "" {
		t.Fatalf("panic must not record a marker, got %q", v)
	}
}
