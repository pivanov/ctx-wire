package shim

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAggregateStatusUnionsAcrossDirs covers the multi-dir fix: a shim installed
// in one dir and another shim in a second dir must both count, so a stale earlier
// shim dir is never missed when reasoning about hot-path cost.
func TestAggregateStatusUnionsAcrossDirs(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	realDir := t.TempDir()

	write := func(p string) {
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctxWire := filepath.Join(realDir, "ctx-wire")
	write(ctxWire)
	write(filepath.Join(realDir, "git"))
	write(filepath.Join(realDir, "sort"))

	if _, err := Install(dirA, ctxWire, []string{"git"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dirB, ctxWire, []string{"sort"}); err != nil {
		t.Fatal(err)
	}
	// dirA and dirB before realDir, so git resolves to dirA's shim and sort to dirB's.
	// (git + sort are both coreutils present on any runner; an earlier revision used
	// grep, then rg — rg is not installed on the CI runner, which broke this test.)
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", dirA+sep+dirB+sep+realDir)

	installed, active, _, _ := AggregateStatus([]string{dirA, dirB})
	if installed != 2 {
		t.Errorf("installed across dirs = %d, want 2 (git in A + sort in B)", installed)
	}
	if active != 2 {
		t.Errorf("active across dirs = %d, want 2 (both resolve to a shim)", active)
	}

	// ManagedDirsWith includes a non-PATH install dir without duplicating an existing one.
	if got := ManagedDirsWith(dirA); len(got) == 0 {
		t.Error("ManagedDirsWith should include managed dirs")
	}
}

// TestKeepMarker covers the explicit-intent marker that suppresses the redundant
// shims advisory: a deliberate install (steering init / `shims install`) must read
// as "keep", and uninstall must clear it.
func TestKeepMarker(t *testing.T) {
	dir := t.TempDir()
	if WantsKeep([]string{dir}) {
		t.Fatal("a fresh dir must not report a keep-marker")
	}
	MarkKeep(dir)
	if !WantsKeep([]string{dir}) {
		t.Error("WantsKeep should see the marker after MarkKeep")
	}
	ClearKeep(dir)
	if WantsKeep([]string{dir}) {
		t.Error("WantsKeep should be false after ClearKeep")
	}
}
