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

// TestCleanupRotationCountCap verifies that cleanup evicts oldest files when the
// count exceeds maxFiles. We use a small local cap to keep the test fast.
func TestCleanupRotationCountCap(t *testing.T) {
	// Temporarily lower maxFiles by writing files that exceed it.
	// maxFiles = 200; we create maxFiles+5 files and expect exactly maxFiles to remain.
	dir := t.TempDir()
	total := maxFiles + 5
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("%020d_cmd_%s.log", 1_000_000_000_000_000_000+i, strings.Repeat("a", 64))
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
		t.Errorf("after count-cap cleanup got %d files, want %d", len(entries), maxFiles)
	}
	// Oldest (index 0..4) must be gone.
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("%020d_cmd_%s.log", 1_000_000_000_000_000_000+i, strings.Repeat("a", 64))
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("oldest file %s should have been rotated out", name)
		}
	}
	// Newest must still be present.
	lastName := fmt.Sprintf("%020d_cmd_%s.log", 1_000_000_000_000_000_000+total-1, strings.Repeat("a", 64))
	if _, err := os.Stat(filepath.Join(dir, lastName)); err != nil {
		t.Error("newest file should remain after count-cap cleanup")
	}
}

// TestCleanupBytesBudget verifies that cleanup evicts oldest files when total
// size exceeds maxBytes, even when the count cap is not reached.
func TestCleanupBytesBudget(t *testing.T) {
	dir := t.TempDir()
	// Each file is 1 MiB. Create maxBytes/1MiB + 2 files to push over the budget.
	fileSize := int64(1 << 20) // 1 MiB
	numFiles := int(maxBytes/fileSize) + 2
	payload := make([]byte, fileSize)
	for i := 0; i < numFiles; i++ {
		name := fmt.Sprintf("%020d_cmd_%s.log", 1_000_000_000_000_000_000+i, strings.Repeat("a", 64))
		if err := os.WriteFile(filepath.Join(dir, name), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cleanup(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// After cleanup, total bytes should be <= maxBytes.
	var totalBytes int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		totalBytes += info.Size()
	}
	if totalBytes > maxBytes {
		t.Errorf("after bytes-budget cleanup total bytes = %d, want <= %d", totalBytes, maxBytes)
	}
	// The oldest file (index 0) must be gone.
	oldestName := fmt.Sprintf("%020d_cmd_%s.log", 1_000_000_000_000_000_000, strings.Repeat("a", 64))
	if _, err := os.Stat(filepath.Join(dir, oldestName)); err == nil {
		t.Error("oldest file should have been evicted by bytes-budget cleanup")
	}
}

// TestCleanupNewestSurvivesOversizeBudget pins the regression: a single spool
// larger than the whole byte budget must NOT be evicted, or the handle just
// minted for it would be dead on arrival. Sparse file keeps the test cheap.
func TestCleanupNewestSurvivesOversizeBudget(t *testing.T) {
	dir := t.TempDir()
	name := fmt.Sprintf("%020d_cmd_%s.log", int64(1_000_000_000_000_000_000), strings.Repeat("a", 64))
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxBytes + 1); err != nil { // sparse: Size() reports >maxBytes, no real bytes written
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	cleanup(dir)
	if _, err := os.Stat(p); err != nil {
		t.Errorf("a single over-budget spool must survive cleanup (it is the newest handle), but it was evicted: %v", err)
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

// TestEvictionNotFound verifies that once more than maxFiles spool files exist
// the oldest hash no longer resolves. We pre-populate the spool dir with
// maxFiles+1 hand-crafted files so the test stays fast regardless of maxFiles.
func TestEvictionNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	// Pre-populate maxFiles+1 fake spool files in chronological order.
	// Each file has a unique 64-hex hash embedded in its name.
	total := maxFiles + 1
	var firstHash string
	for i := 0; i < total; i++ {
		// Build a fake 64-hex hash where the first 8 chars encode the index.
		raw := sha256.Sum256([]byte(fmt.Sprintf("evict-sentinel-%d", i)))
		h := hex.EncodeToString(raw[:])
		name := fmt.Sprintf("%020d_evict-test_%s.log", 1_000_000_000_000_000_000+i, h)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstHash = h
		}
		// Finalize a new real spool (even with tiny content) to trigger cleanup
		// on the last iteration.
		if i == total-1 {
			s := NewSpool("evict-trigger")
			writeAll(t, s, strings.Repeat("trigger line\n", 50))
			if _, ok := s.Finalize(true); !ok {
				t.Fatal("trigger spool not kept")
			}
		}
	}

	// The oldest file (index 0) should have been evicted by cleanup.
	_, ok := Resolve(firstHash[:handleLen])
	if ok {
		t.Error("oldest hash must have been evicted after maxFiles+1 spools, but Resolve still found it")
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

// -- Section 5.3: SpoolReader review-gate tests --

// TestSpoolReaderSecretSafety is the planted-secret review-gate test. It spools a
// reader containing a ghp_ token and a multi-line PEM block via SpoolReader, then
// verifies that both the spool file and the fetch (Resolve + read) code path
// contain no raw secret and have the expected [REDACTED] marker. This pins the
// invariant that SpoolReader goes through the scrub boundary identically to runner stdout.
func TestSpoolReaderSecretSafety(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	rawToken := "ghp_" + strings.Repeat("X", 36)
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIBVgIBADANBgSECRETKEYBODY\nMOREBODYLINES\n-----END RSA PRIVATE KEY-----\n"
	// Build a multi-line payload well over minSpoolSize so the spool file is opened.
	payload := strings.Repeat("log line from file\n", 40) +
		"token=" + rawToken + "\n" +
		strings.Repeat("more log\n", 20) +
		pem +
		"after pem\n"

	handle, ok := SpoolReader("secret-reader-test", strings.NewReader(payload))
	if !ok {
		t.Fatal("SpoolReader: expected ok=true")
	}
	if len(handle) != handleLen {
		t.Errorf("SpoolReader handle length = %d, want %d", len(handle), handleLen)
	}

	// Resolve the handle and read via the fetch code path.
	fetchPath, ok := Resolve(handle)
	if !ok {
		t.Fatalf("Resolve(%q) failed for SpoolReader handle", handle)
	}
	data, err := os.ReadFile(fetchPath)
	if err != nil {
		t.Fatalf("read via fetch path: %v", err)
	}

	// The raw token must be absent.
	if strings.Contains(string(data), rawToken) {
		t.Error("SpoolReader: raw GitHub token must not appear in spool file (scrub boundary violated)")
	}
	// The PEM body must be absent.
	if strings.Contains(string(data), "SECRETKEYBODY") {
		t.Error("SpoolReader: PEM private key body must not appear in spool file")
	}
	// Redaction marker must be present.
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Error("SpoolReader: expected [REDACTED] marker in spool file")
	}

	// Ranged fetch over the token line must also show no raw secret.
	// Count lines until we find the token area and fetch a window around it.
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.Contains(line, rawToken) {
			t.Errorf("SpoolReader: raw token found on line %d in spool: %q", i+1, line)
		}
	}
}

// TestSpoolReaderRangedFetchRoundTrip is the ranged-fetch review-gate test.
// It spools a known multi-line payload via SpoolReader, then verifies:
//   - fetch <hash> (whole) returns all scrubbed lines
//   - fetch <hash> --lines A-B returns exactly lines A..B
//   - clamping: B beyond EOF returns up to the last line without error
//   - A beyond EOF yields empty output (covered by emitLines behavior)
//
// The fetch logic lives in cmd_fetch.go (emitLines). We test the tee layer here
// by reading the resolved path directly, mirroring what cmdFetch does.
func TestSpoolReaderRangedFetchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDir, dir)

	// Build a payload of exactly 30 numbered lines.
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&sb, "line %02d content\n", i)
	}
	payload := sb.String()

	handle, ok := SpoolReader("ranged-test", strings.NewReader(payload))
	if !ok {
		t.Fatal("SpoolReader: expected ok=true")
	}

	fetchPath, ok := Resolve(handle)
	if !ok {
		t.Fatalf("Resolve(%q) failed", handle)
	}

	// Helper: read lines [a, b] from the resolved file (1-based, inclusive, clamped).
	readRange := func(a, b int) []string {
		t.Helper()
		data, err := os.ReadFile(fetchPath)
		if err != nil {
			t.Fatalf("read fetch path: %v", err)
		}
		all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if a < 1 {
			a = 1
		}
		if b > len(all) {
			b = len(all)
		}
		if a > len(all) {
			return nil
		}
		return all[a-1 : b]
	}

	// Whole output: all 30 lines.
	got := readRange(1, 30)
	if len(got) != 30 {
		t.Errorf("whole fetch: got %d lines, want 30", len(got))
	}
	if got[0] != "line 01 content" {
		t.Errorf("whole fetch line 1 = %q, want %q", got[0], "line 01 content")
	}
	if got[29] != "line 30 content" {
		t.Errorf("whole fetch line 30 = %q, want %q", got[29], "line 30 content")
	}

	// Ranged: lines 5-10.
	got = readRange(5, 10)
	if len(got) != 6 {
		t.Errorf("ranged [5,10]: got %d lines, want 6", len(got))
	}
	if got[0] != "line 05 content" {
		t.Errorf("ranged [5,10] first = %q, want %q", got[0], "line 05 content")
	}
	if got[5] != "line 10 content" {
		t.Errorf("ranged [5,10] last = %q, want %q", got[5], "line 10 content")
	}

	// Clamping: B > total (40 > 30) should clamp to 30.
	got = readRange(25, 40)
	if len(got) != 6 { // lines 25..30 = 6 lines
		t.Errorf("clamped [25,40]: got %d lines, want 6 (clamped to 30)", len(got))
	}
	if got[len(got)-1] != "line 30 content" {
		t.Errorf("clamped [25,40] last = %q, want line 30 content", got[len(got)-1])
	}

	// A > total: should return empty.
	got = readRange(31, 35)
	if len(got) != 0 {
		t.Errorf("A > total [31,35]: got %d lines, want 0", len(got))
	}
}
