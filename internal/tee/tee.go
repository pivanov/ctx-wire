// Package tee persists full command output to disk so the operator can recover
// detail the filtered summary dropped. It is the persistence boundary for the
// secret-redaction guarantee: output is scrubbed in-flight by a streaming
// scrubber before it is ever written, so raw bytes never touch disk.
//
// A Spool streams output to a temp file as the command runs (bounded memory,
// full output preserved even past the in-memory cap) and is kept on disk only
// when the command failed or output was truncated; otherwise it is discarded.
package tee

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ctx-wire/internal/paths"
	"ctx-wire/internal/scrub"
)

const (
	minSpoolSize = 500            // below this, output needs no recovery file
	maxFiles     = 20             // keep at most this many spool files
	envDisable   = "CTX_WIRE_TEE" // set to "0" to disable spooling
	envDir       = "CTX_WIRE_TEE_DIR"
	envFallback  = "CTX_WIRE_TEE_FALLBACK_DIR"
)

// Spool streams scrubbed command output to a file. It is safe for concurrent
// use by the stdout and stderr copiers. The file is created lazily once output
// crosses minSpoolSize, so trivial commands never touch disk.
type Spool struct {
	slug     string
	disabled bool

	mu     sync.Mutex
	head   []byte
	opened bool
	path   string
	file   *os.File
	sw     *scrub.Writer
	err    error
}

// NewSpool returns a spool for a command identified by slug. Writes are no-ops
// if spooling is disabled via CTX_WIRE_TEE=0.
func NewSpool(slug string) *Spool {
	return &Spool{slug: slug, disabled: os.Getenv(envDisable) == "0"}
}

// Write streams bytes into the spool, scrubbing them in-flight. It always
// reports a full write and swallows I/O errors so it never disrupts the command
// or the copier goroutines; a failed spool simply yields no recovery file.
func (s *Spool) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled || s.err != nil {
		return len(p), nil
	}
	if !s.opened {
		s.head = append(s.head, p...)
		if len(s.head) < minSpoolSize {
			return len(p), nil
		}
		if err := s.open(); err != nil {
			s.err = err
			return len(p), nil
		}
		if _, err := s.sw.Write(s.head); err != nil {
			s.err = err
		}
		s.head = nil
		return len(p), nil
	}
	if _, err := s.sw.Write(p); err != nil {
		s.err = err
	}
	return len(p), nil
}

func (s *Spool) open() error {
	dirs, err := spoolDirs()
	if err != nil {
		return err
	}
	var lastErr error
	for _, dir := range dirs {
		if err := s.openInDir(dir); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func (s *Spool) openInDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pattern := fmt.Sprintf("%d_%s_*.log", time.Now().UnixNano(), sanitizeSlug(s.slug))
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	s.file = f
	s.path = f.Name()
	s.sw = scrub.NewWriter(f)
	s.opened = true
	return nil
}

// Finalize flushes and closes the spool. Pass keep=true to retain the file
// (command failed or output was truncated); otherwise it is removed. It returns
// the file path and ok=true only when a file was retained.
func (s *Spool) Finalize(keep bool) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.opened {
		if keep && len(s.head) > 0 && !s.disabled && s.err == nil {
			if err := s.open(); err != nil {
				return "", false
			}
			if _, err := s.sw.Write(s.head); err != nil {
				s.err = err
			}
			s.head = nil
		}
	}
	if !s.opened {
		return "", false
	}
	flushErr := s.sw.Close()
	_ = s.file.Close()
	if !keep || s.err != nil || flushErr != nil {
		_ = os.Remove(s.path)
		return "", false
	}
	cleanup(filepath.Dir(s.path))
	return s.path, true
}

// Hint formats the recovery pointer appended to filtered output.
func Hint(path string) string {
	return fmt.Sprintf("[full output: %s]", displayPath(path))
}

// PrimaryDir returns the primary spool directory for the current environment.
// Exposed read-only for diagnostics (ctx-wire doctor).
func PrimaryDir() (string, error) {
	if d := os.Getenv(envDir); d != "" {
		return d, nil
	}
	return primarySpoolDir()
}

// WriteDirs returns the candidate spool directories in the same priority order
// the spool uses at runtime: primary first, then fallback (omitted when an
// explicit CTX_WIRE_TEE_DIR override is set). Exposed read-only for diagnostics.
func WriteDirs() ([]string, error) {
	return spoolDirs()
}

func spoolDirs() ([]string, error) {
	if d := os.Getenv(envDir); d != "" {
		return []string{d}, nil
	}
	primary, err := primarySpoolDir()
	if err != nil {
		return nil, err
	}
	fallback, err := fallbackSpoolDir()
	if err != nil {
		return nil, err
	}
	if fallback == primary {
		return []string{primary}, nil
	}
	return []string{primary, fallback}, nil
}

func primarySpoolDir() (string, error) {
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", "tee"), nil
}

func fallbackSpoolDir() (string, error) {
	if d := os.Getenv(envFallback); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "unknown"
	}
	sum := sha256.Sum256([]byte(home))
	dir := fmt.Sprintf("ctx-wire-%x", sum[:4])
	return filepath.Join(os.TempDir(), dir, "tee"), nil
}

// sanitizeSlug keeps alphanumerics, underscore, and hyphen; other runes become
// underscore. The result is truncated to 40 runes.
func sanitizeSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len([]rune(out)) > 40 {
		out = string([]rune(out)[:40])
	}
	return out
}

// cleanup keeps only the newest maxFiles .log files in dir.
func cleanup(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var logs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	if len(logs) <= maxFiles {
		return
	}
	sort.Strings(logs) // filenames begin with epoch seconds => chronological
	for _, name := range logs[:len(logs)-maxFiles] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}
