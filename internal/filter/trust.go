package filter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"ctx-wire/internal/paths"
)

// Trust gates project-local filters. A project's .ctx-wire/filters.toml is only
// loaded after the user approves it with `ctx-wire trust`, which records the
// file's SHA-256 in a trust store keyed by absolute path. If the file changes,
// its hash no longer matches and it is treated as untrusted until re-approved.

func dataDir() (string, error) {
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire"), nil
}

func trustStorePath() (string, error) {
	d, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "trust.json"), nil
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// loadTrustStore reads the path->hash map. A missing or corrupt store yields an
// empty map (fail-closed for trust: nothing is trusted), never an error.
func loadTrustStore() map[string]string {
	p, err := trustStorePath()
	if err != nil {
		return map[string]string{}
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return map[string]string{}
	}
	m := map[string]string{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m); err != nil {
			return map[string]string{}
		}
	}
	return m
}

// IsTrusted reports whether the file at path has been approved and its contents
// are unchanged since approval.
func IsTrusted(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	want, ok := loadTrustStore()[abs]
	if !ok {
		return false
	}
	got, err := fileHash(path)
	if err != nil {
		return false
	}
	return got == want
}

// Trust states reported by TrustState.
const (
	TrustAbsent    = "absent"    // no file at path
	TrustUntrusted = "untrusted" // file exists but was never approved
	TrustChanged   = "changed"   // approved once, but contents differ now
	TrustTrusted   = "trusted"   // approved and unchanged
)

// TrustState reports the trust status of the file at path, distinguishing
// never-approved from approved-then-changed (both of which IsTrusted reports as
// false). It is read-only and used for diagnostics.
func TrustState(path string) string {
	if _, err := os.Stat(path); err != nil {
		return TrustAbsent
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return TrustUntrusted
	}
	want, ok := loadTrustStore()[abs]
	if !ok {
		return TrustUntrusted
	}
	got, err := fileHash(path)
	if err != nil {
		return TrustUntrusted
	}
	if got == want {
		return TrustTrusted
	}
	return TrustChanged
}

// Revoke removes the file at path from the trust store, so its project filters
// are no longer loaded until re-approved. It reports whether an entry was
// actually present and removed. The store file is deleted when it becomes empty.
func Revoke(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	store := loadTrustStore()
	if _, ok := store[abs]; !ok {
		return false, nil
	}
	delete(store, abs)

	p, err := trustStorePath()
	if err != nil {
		return false, err
	}
	if len(store) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		return true, nil
	}
	out, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(p, append(out, '\n'), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// Approve records the current SHA-256 of the file at path as trusted. It
// returns the recorded hash.
func Approve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	h, err := fileHash(path)
	if err != nil {
		return "", err
	}
	store := loadTrustStore()
	store[abs] = h

	p, err := trustStorePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, append(out, '\n'), 0o600); err != nil {
		return "", err
	}
	return h, nil
}
