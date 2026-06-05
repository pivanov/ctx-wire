package tee

import (
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
