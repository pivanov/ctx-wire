package filter

import (
	"os"
	"path/filepath"
	"testing"
)

// isolateEnv points HOME/XDG at temp dirs so user-filter and trust-store lookups
// are hermetic, and returns a fresh project dir.
func isolateEnv(t *testing.T) (projectDir, userPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	return t.TempDir(), filepath.Join(home, ".config", "ctx-wire", "filters.toml")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const makeFilter = `schema_version = 1
[filters.make]
match_command = "^make\\b"
description = %q
`

func TestLoadTrustedProjectOverride(t *testing.T) {
	proj, _ := isolateEnv(t)
	ppath := ProjectFiltersPath(proj)
	writeFile(t, ppath, "schema_version = 1\n[filters.make]\nmatch_command = \"^make\\\\b\"\ndescription = \"PROJECT-MAKE\"\n")
	if _, err := Approve(ppath); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	reg, err := Load(proj)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := reg.Find("make all")
	if f == nil || f.Description != "PROJECT-MAKE" {
		t.Errorf("trusted project filter did not win: got %+v", f)
	}
}

func TestLoadUntrustedProjectIgnored(t *testing.T) {
	proj, _ := isolateEnv(t)
	ppath := ProjectFiltersPath(proj)
	writeFile(t, ppath, "schema_version = 1\n[filters.make]\nmatch_command = \"^make\\\\b\"\ndescription = \"PROJECT-MAKE\"\n")
	// Not approved.

	reg, err := Load(proj)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := reg.Find("make all")
	if f == nil {
		t.Fatal("expected built-in make to match")
	}
	if f.Description == "PROJECT-MAKE" {
		t.Error("untrusted project filter was applied")
	}
}

func TestLoadUserGlobalOverride(t *testing.T) {
	proj, userPath := isolateEnv(t)
	writeFile(t, userPath, "schema_version = 1\n[filters.make]\nmatch_command = \"^make\\\\b\"\ndescription = \"USER-MAKE\"\n")

	reg, err := Load(proj)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := reg.Find("make all")
	if f == nil || f.Description != "USER-MAKE" {
		t.Errorf("user-global filter did not win: got %+v", f)
	}
}

func TestLoadBuiltinFallback(t *testing.T) {
	proj, _ := isolateEnv(t)
	reg, err := Load(proj)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f := reg.Find("make all"); f == nil || f.Name != "make" {
		t.Errorf("expected built-in make, got %+v", f)
	}
	if f := reg.Find("some-unknown-tool xyz"); f != nil {
		t.Errorf("expected passthrough for unknown command, got %q", f.Name)
	}
}

func TestLoadInvalidProjectFilterFallsOpen(t *testing.T) {
	proj, _ := isolateEnv(t)
	ppath := ProjectFiltersPath(proj)
	writeFile(t, ppath, "this is not valid toml at all !!! [[[")
	if _, err := Approve(ppath); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	reg, err := Load(proj)
	if err != nil {
		t.Fatalf("Load must not fail on a broken project file: %v", err)
	}
	if f := reg.Find("make all"); f == nil || f.Name != "make" {
		t.Errorf("built-in make should still load when project file is invalid, got %+v", f)
	}
}
