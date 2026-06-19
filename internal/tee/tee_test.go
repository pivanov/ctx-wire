package tee

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeAll(t *testing.T, s *Spool, data string) {
	t.Helper()
	if _, err := s.Write([]byte(data)); err != nil {
		t.Fatalf("Spool.Write: %v", err)
	}
}

func TestSpoolBelowThresholdDiscardNoFile(t *testing.T) {
	t.Setenv(envDir, t.TempDir())
	s := NewSpool("cmd")
	writeAll(t, s, "tiny output")
	path, ok := s.Finalize(false)
	if ok || path != "" {
		t.Errorf("expected no file below threshold, got %q ok=%v", path, ok)
	}
}

func TestSpoolKeepBelowThreshold(t *testing.T) {
	t.Setenv(envDir, t.TempDir())
	s := NewSpool("cmd")
	writeAll(t, s, "tiny output")
	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected small kept spool file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if string(data) != "tiny output" {
		t.Errorf("spool = %q, want tiny output", data)
	}
}

func TestSpoolKeepOnFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)
	s := NewSpool("deploy")
	writeAll(t, s, strings.Repeat("output line\n", 100))
	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file to be kept on failure")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("spool file missing: %v", err)
	}
}

func TestSpoolNamesAreUniqueForRepeatedCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)
	first := NewSpool("same command")
	second := NewSpool("same command")
	writeAll(t, first, strings.Repeat("output line\n", 100))
	writeAll(t, second, strings.Repeat("output line\n", 100))
	path1, ok1 := first.Finalize(true)
	path2, ok2 := second.Finalize(true)
	if !ok1 || !ok2 {
		t.Fatalf("expected both spool files to be kept: ok1=%v ok2=%v", ok1, ok2)
	}
	if path1 == path2 {
		t.Fatalf("spool paths collided: %q", path1)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("spool file count = %d, want 2", len(entries))
	}
}

func TestSpoolFallsBackWhenPrimaryUnavailable(t *testing.T) {
	dir := t.TempDir()
	primaryBase := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(primaryBase, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(dir, "fallback", "tee")
	t.Setenv(envDir, "")
	t.Setenv(envFallback, fallback)
	t.Setenv("XDG_DATA_HOME", primaryBase)

	s := NewSpool("deploy")
	writeAll(t, s, strings.Repeat("output line\n", 100))
	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file in fallback dir")
	}
	if !strings.HasPrefix(path, fallback+string(os.PathSeparator)) {
		t.Fatalf("spool path = %q, want fallback under %q", path, fallback)
	}
}

func TestSpoolDiscardOnSuccess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)
	s := NewSpool("build")
	writeAll(t, s, strings.Repeat("output line\n", 100))
	if path, ok := s.Finalize(false); ok || path != "" {
		t.Errorf("expected spool discarded on success, got %q ok=%v", path, ok)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected empty spool dir, got %d entries", len(entries))
	}
}

func TestSpoolScrubsMultiLineSecret(t *testing.T) {
	t.Setenv(envDir, t.TempDir())
	s := NewSpool("connect")
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIBVgIBADANBgSECRETKEYBODY\n-----END RSA PRIVATE KEY-----\n"
	// Pad so the spool crosses the threshold and opens.
	writeAll(t, s, strings.Repeat("connecting to host\n", 40)+pem)
	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if strings.Contains(string(data), "SECRETKEYBODY") {
		t.Error("multi-line secret leaked into spool file")
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Error("expected redaction marker in spool file")
	}
}

func TestSpoolConcurrentWrites(t *testing.T) {
	t.Setenv(envDir, t.TempDir())
	s := NewSpool("parallel")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				writeAll(t, s, fmt.Sprintf("stream %d line %d\n", n, j))
			}
		}(i)
	}
	wg.Wait()
	if _, ok := s.Finalize(true); !ok {
		t.Fatal("expected spool file from concurrent writers")
	}
}

func TestCleanupRotation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("%010d_cmd.log", 1_000_000+i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cleanup(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxFiles {
		t.Errorf("after cleanup got %d files, want %d", len(entries), maxFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "0001000000_cmd.log")); err == nil {
		t.Error("oldest file should have been rotated out")
	}
	if _, err := os.Stat(filepath.Join(dir, "0001000024_cmd.log")); err != nil {
		t.Error("newest file should remain")
	}
}

func TestSanitizeSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"cargo test", "cargo_test"},
		{"go/test/./pkg", "go_test___pkg"},
		{"plain-name", "plain-name"},
	}
	for _, tt := range tests {
		if got := sanitizeSlug(tt.in); got != tt.want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := sanitizeSlug(strings.Repeat("a", 60)); len([]rune(got)) != 40 {
		t.Errorf("slug not truncated to 40: len=%d", len([]rune(got)))
	}
}

// -- Section 5.1: Content addressing --

// TestHashStableMatchesContent spools a known payload, confirms the file is
// renamed to embed the sha256 of its scrubbed content, and that Hint renders the
// "ctx-wire fetch <12hex>" form.
func TestHashStableMatchesContent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)
	s := NewSpool("hash-test")
	payload := strings.Repeat("content line\n", 50) // well over minSpoolSize
	writeAll(t, s, payload)
	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file to be kept")
	}
	// The filename must end with "_<64hex>.log".
	h := hashFromName(path)
	if h == "" {
		t.Fatalf("path %q does not embed a 64-hex hash", path)
	}
	// The hash must match sha256 of the file contents on disk.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	if h != want {
		t.Errorf("embedded hash = %s, want sha256(fileContents) = %s", h, want)
	}
	// Hint must render the stable handle.
	hint := Hint(path)
	if !strings.HasPrefix(hint, "[full output: ctx-wire fetch ") {
		t.Errorf("Hint = %q, want ctx-wire fetch form", hint)
	}
	if !strings.HasSuffix(hint, "]") {
		t.Errorf("Hint = %q, missing closing bracket", hint)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(hint, "[full output: ctx-wire fetch "), "]")
	if len(inner) != handleLen {
		t.Errorf("handle length = %d, want %d: %q", len(inner), handleLen, inner)
	}
	if inner != h[:handleLen] {
		t.Errorf("handle = %q, want first %d hex chars of hash %q", inner, handleLen, h)
	}
}

// TestResolveRoundTrips confirms Resolve returns the same path for both the
// short handle (12 hex chars) and the full 64-hex hash.
func TestResolveRoundTrips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)
	s := NewSpool("resolve-test")
	writeAll(t, s, strings.Repeat("resolve line\n", 50))
	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file")
	}
	h := hashFromName(path)
	if h == "" {
		t.Fatalf("path %q has no embedded hash", path)
	}

	// Resolve with the 12-char handle.
	got, ok := Resolve(h[:handleLen])
	if !ok {
		t.Fatalf("Resolve(%q) not found", h[:handleLen])
	}
	if got != path {
		t.Errorf("Resolve short: got %q, want %q", got, path)
	}

	// Resolve with the full 64-hex hash.
	got, ok = Resolve(h)
	if !ok {
		t.Fatalf("Resolve(%q) full hash not found", h)
	}
	if got != path {
		t.Errorf("Resolve full: got %q, want %q", got, path)
	}
}

// TestEvictionNotFound verifies that after 21 distinct spools the oldest hash
// no longer resolves (maxFiles=20 cleanup evicts it).
func TestEvictionNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	var firstHash string
	for i := 0; i < 21; i++ {
		s := NewSpool("evict-test")
		// Each payload must differ so each spool has a unique hash.
		writeAll(t, s, strings.Repeat(fmt.Sprintf("evict line %d\n", i), 50))
		path, ok := s.Finalize(true)
		if !ok {
			t.Fatalf("spool %d not kept", i)
		}
		if i == 0 {
			firstHash = hashFromName(path)
			if firstHash == "" {
				t.Fatalf("spool 0 has no embedded hash: %q", path)
			}
		}
	}

	// The oldest spool (i=0) should have been evicted by cleanup.
	_, ok := Resolve(firstHash[:handleLen])
	if ok {
		t.Error("oldest hash must have been evicted after 21 spools, but Resolve still found it")
	}
}

// TestResolveAmbiguousPrefixReturnsNotFound verifies that a prefix matching two
// distinct full hashes returns ok=false.
func TestResolveAmbiguousPrefixReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	// Construct two fake spool filenames with different full hashes that share
	// the prefix "00000000". We hand-craft names to control the hashes directly.
	hash1 := "0000000011111111222222223333333344444444555555556666666677777777"
	hash2 := "0000000088888888999999990000000011111111222222223333333444444444"
	for _, h := range []string{hash1, hash2} {
		name := fmt.Sprintf("1000000000000000000_test_%s.log", h)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, ok := Resolve("00000000") // prefix matches both hashes
	if ok {
		t.Error("Resolve must return ok=false when prefix spans two distinct full hashes")
	}
}

// TestResolveDuplicateSameHashReturnsNewest is the regression test for fix #3
// from the plan: two spool files with the same full hash (e.g. identical content
// spooled twice) must NOT be treated as ambiguous. Resolve must return ok=true
// and yield the newest (largest-epoch) file.
func TestResolveDuplicateSameHashReturnsNewest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	sharedHash := "aabbccdd11223344aabbccdd11223344aabbccdd11223344aabbccdd11223344"
	// Two files with the same hash but different epoch prefixes.
	olderName := fmt.Sprintf("1000000000000000000_test_%s.log", sharedHash)
	newerName := fmt.Sprintf("2000000000000000000_test_%s.log", sharedHash)
	content := []byte("scrubbed content")
	for _, name := range []string{olderName, newerName} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	path, ok := Resolve(sharedHash[:handleLen])
	if !ok {
		t.Fatal("Resolve must return ok=true for duplicate same-hash files")
	}
	if filepath.Base(path) != newerName {
		t.Errorf("Resolve returned %q, want the newer file %q", filepath.Base(path), newerName)
	}
}

// -- Section 5.2: Secret-safety --

// TestFetchSingleLineSecretAbsent confirms that after spooling output containing
// a recognizable single-line secret (GitHub token pattern), the spool file and
// the bytes returned by the fetch code path both lack the raw secret.
func TestFetchSingleLineSecretAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	// A GitHub personal access token shape: "ghp_" + 36 alphanumeric chars.
	rawSecret := "ghp_" + strings.Repeat("A", 36)
	payload := strings.Repeat("connecting to github\n", 30) + "token=" + rawSecret + "\n"

	s := NewSpool("secret-test")
	writeAll(t, s, payload)
	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file")
	}

	// Read via the fetch code path (open the Resolve'd path).
	fetchPath, ok := Resolve(hashFromName(path)[:handleLen])
	if !ok {
		t.Fatal("Resolve failed for newly spooled file")
	}
	data, err := os.ReadFile(fetchPath)
	if err != nil {
		t.Fatalf("read via fetch path: %v", err)
	}
	if strings.Contains(string(data), rawSecret) {
		t.Error("raw secret must not appear in the spool file")
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Error("expected redaction marker in spool file")
	}
}

// TestFetchMultiLinePEMAbsent confirms that a PEM private key block split across
// multiple Write calls does not appear in the spool file.
// This exercises scrub.Writer's hold-back at writer.go:79-145.
func TestFetchMultiLinePEMAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	s := NewSpool("pem-test")
	// Pad to exceed minSpoolSize so the file is opened before the PEM arrives.
	writeAll(t, s, strings.Repeat("log line\n", 60))
	// Write the PEM block in three separate Write calls to test hold-back.
	writeAll(t, s, "-----BEGIN RSA PRIVATE KEY-----\n")
	writeAll(t, s, "MIIBVgIBADANBgSECRETKEYBODY\nMOREBODYLINES\n")
	writeAll(t, s, "-----END RSA PRIVATE KEY-----\n")
	writeAll(t, s, "after pem line\n")

	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if strings.Contains(string(data), "SECRETKEYBODY") {
		t.Error("PEM private key body must not appear in spool file")
	}
	if strings.Contains(string(data), "MOREBODYLINES") {
		t.Error("PEM private key body (line 2) must not appear in spool file")
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Error("expected redaction marker in spool file after PEM block")
	}
	if !strings.Contains(string(data), "after pem line") {
		t.Error("content after the PEM block must be preserved")
	}
}

// TestHashIsOfScrubbedBytes confirms that the embedded hash in the filename
// equals sha256 of the scrubbed file contents, NOT of the raw input. This is the
// tripwire test: if the io.MultiWriter tee is moved upstream of scrub.NewWriter,
// the hashes will diverge and this test will fail.
func TestHashIsOfScrubbedBytes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	// Use a recognizable secret so the raw vs. scrubbed content differs.
	rawSecret := "ghp_" + strings.Repeat("B", 36)
	payload := strings.Repeat("output line\n", 40) + "token=" + rawSecret + "\n"

	s := NewSpool("hash-scrub-test")
	writeAll(t, s, payload)
	path, ok := s.Finalize(true)
	if !ok {
		t.Fatal("expected spool file")
	}

	h := hashFromName(path)
	if h == "" {
		t.Fatalf("path %q has no embedded hash", path)
	}

	// The hash must equal sha256 of the scrubbed file on disk.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	sum := sha256.Sum256(data)
	wantHash := hex.EncodeToString(sum[:])
	if h != wantHash {
		t.Errorf("embedded hash = %s, sha256(scrubbed file) = %s (hash is not of scrubbed bytes)", h, wantHash)
	}

	// Confirm the file is scrubbed (double-check secret absence).
	if strings.Contains(string(data), rawSecret) {
		t.Error("raw secret must not appear in spool: hash-is-of-scrubbed-bytes tripwire only works when the file is actually scrubbed")
	}
}
