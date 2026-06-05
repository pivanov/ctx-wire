package filter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRevoke(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir) // trust store lives under dataDir()

	proj := t.TempDir()
	fp := filepath.Join(proj, "filters.toml")
	if err := os.WriteFile(fp, []byte("schema_version=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Revoking an untrusted path is a no-op, not an error.
	if removed, err := Revoke(fp); err != nil || removed {
		t.Fatalf("Revoke(untrusted) = (%v, %v), want (false, nil)", removed, err)
	}

	if _, err := Approve(fp); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !IsTrusted(fp) {
		t.Fatal("file should be trusted after Approve")
	}

	removed, err := Revoke(fp)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !removed {
		t.Fatal("Revoke should report the entry was removed")
	}
	if IsTrusted(fp) {
		t.Fatal("file must not be trusted after Revoke")
	}
	if TrustState(fp) != TrustUntrusted {
		t.Errorf("TrustState = %q, want %q", TrustState(fp), TrustUntrusted)
	}

	// Store file is removed once empty.
	if p, _ := trustStorePath(); p != "" {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("trust store should be gone when empty, stat err = %v", err)
		}
	}
}

func TestRevokeKeepsOtherEntries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	mk := func() string {
		p := filepath.Join(t.TempDir(), "filters.toml")
		if err := os.WriteFile(p, []byte("schema_version=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Approve(p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := mk(), mk()

	if _, err := Revoke(a); err != nil {
		t.Fatal(err)
	}
	if IsTrusted(a) {
		t.Error("a should be untrusted")
	}
	if !IsTrusted(b) {
		t.Error("b must stay trusted after revoking a")
	}
}
