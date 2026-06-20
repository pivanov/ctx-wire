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
	"encoding/hex"
	"fmt"
	"hash"
	"io"
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
	maxFiles     = 200            // keep at most this many spool files (raised from 20; SpoolReader enables frequent spooling)
	maxBytes     = 50 << 20       // 50 MiB total spool budget; evict oldest-first when exceeded
	envDisable   = "CTX_WIRE_TEE" // set to "0" to disable spooling
	envDir       = "CTX_WIRE_TEE_DIR"
	envFallback  = "CTX_WIRE_TEE_FALLBACK_DIR"

	// handleLen is the number of hex characters shown in the ctx-wire fetch hint.
	// 12 hex chars = 48 bits; the bytes budget (maxBytes=50MiB) is far below the
	// collision threshold for a 48-bit space, so the handle length is correct as-is.
	handleLen = 12
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
	hasher hash.Hash // sha256 of the scrubbed bytes (nil until opened)
	err    error
}

// NewSpool returns a spool for a command identified by slug. Writes are no-ops
// if spooling is disabled via CTX_WIRE_TEE=0.
func NewSpool(slug string) *Spool {
	return &Spool{slug: slug, disabled: os.Getenv(envDisable) == "0"}
}

// SpoolReader content-addresses an arbitrary reader through the same scrub
// boundary as runner stdout. The reader is streamed through scrub.Writer into a
// new Spool; the spool is always kept (keep=true). On success it returns the
// 12-hex handle (matching the length shown in Hint) and ok=true. On any error
// (storage unavailable, CTX_WIRE_TEE=0) it returns ("", false) silently.
//
// SpoolReader MUST be used whenever a file's contents need to be spool-and-fetch
// addressable, because it guarantees secrets are scrubbed before touching disk,
// identically to how runner stdout is scrubbed.
func SpoolReader(slug string, r io.Reader) (handle string, ok bool) {
	s := NewSpool(slug)
	if _, err := io.Copy(s, r); err != nil {
		return "", false
	}
	path, kept := s.Finalize(true)
	if !kept {
		return "", false
	}
	h := hashFromName(path)
	if h == "" {
		return "", false
	}
	return h[:handleLen], true
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
	s.hasher = sha256.New()
	s.sw = scrub.NewWriter(io.MultiWriter(f, s.hasher)) // scrub FIRST, then tee to file + hasher
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
	if s.hasher != nil {
		sum := hex.EncodeToString(s.hasher.Sum(nil)) // full 64-hex of the scrubbed bytes
		dst := hashedName(s.path, sum)
		if dst != s.path {
			if err := os.Rename(s.path, dst); err != nil {
				// Rename failed: keep the original name. Hint falls back to the path form
				// (hashFromName=="" handles it). Recovery still works, just without the
				// stable handle.
				cleanup(filepath.Dir(s.path))
				return s.path, true
			}
			s.path = dst
		}
	}
	cleanup(filepath.Dir(s.path))
	return s.path, true
}

// Hint formats the recovery pointer appended to filtered output. When the spool
// file is content-addressed it renders a stable "ctx-wire fetch <hash>" handle;
// if the hash could not be embedded (rename failure) it falls back to the path.
func Hint(path string) string {
	if h := hashFromName(path); h != "" {
		return fmt.Sprintf("[full output: ctx-wire fetch %s]", h[:handleLen])
	}
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

// cleanup enforces the count cap (maxFiles) and the total-bytes budget (maxBytes)
// by evicting the oldest spool files. Filenames begin with an epoch-nanos prefix,
// so sort.Strings already produces chronological order; oldest files come first.
func cleanup(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type logEntry struct {
		name string
		size int64
	}
	var logs []logEntry
	var totalBytes int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		logs = append(logs, logEntry{name: e.Name(), size: info.Size()})
		totalBytes += info.Size()
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].name < logs[j].name }) // oldest first
	// Evict oldest-first, but never the newest file: a single spool larger than
	// maxBytes (e.g. a huge failing-command log) must survive, or the handle we
	// just minted would be dead on arrival. The byte budget is therefore a
	// best-effort cap that always keeps at least the most recent entry.
	for len(logs) > 1 && (len(logs) > maxFiles || totalBytes > maxBytes) {
		oldest := logs[0]
		_ = os.Remove(filepath.Join(dir, oldest.name))
		totalBytes -= oldest.size
		logs = logs[1:]
	}
}

// hashedName turns ".../1700000000_go_test_123456.log" into
// ".../1700000000_go_test_<sum>.log". The epoch prefix is preserved so cleanup()
// still sorts chronologically; the trailing CreateTemp randomness is replaced by
// the content hash so the file is resolvable by "fetch".
func hashedName(path, sum string) string {
	dir := filepath.Dir(path)
	base := strings.TrimSuffix(filepath.Base(path), ".log")
	// base is "<epoch>_<slug>_<rand>"; keep everything up to the last "_".
	if i := strings.LastIndex(base, "_"); i >= 0 {
		base = base[:i]
	}
	return filepath.Join(dir, base+"_"+sum+".log")
}

// hashFromName extracts the full 64-hex sha256 from a hashed spool filename.
// Returns "" if the trailing segment is not exactly 64 lowercase hex characters
// (i.e. the file was not renamed to the content-addressed form).
func hashFromName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".log")
	i := strings.LastIndex(base, "_")
	if i < 0 {
		return ""
	}
	seg := base[i+1:]
	if len(seg) != 64 {
		return ""
	}
	for _, c := range seg {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return ""
		}
	}
	return seg
}

// Resolve finds the spool file for a hash prefix across the same directories the
// spool writes to. The prefix is AMBIGUOUS only when it matches two or more
// DISTINCT full hashes; multiple files that share the SAME full hash are
// equivalent recovery copies (content-addressing can leave duplicates across
// spool dirs or before a rename), so the newest is returned, not rejected.
// Returns ok=false when nothing matches (evicted/unknown) or the prefix spans
// distinct full hashes.
func Resolve(hashPrefix string) (path string, ok bool) {
	dirs, err := spoolDirs()
	if err != nil {
		return "", false
	}
	full := "" // the single full hash the prefix has resolved to so far
	best := "" // newest matching path (filenames start with epoch nanos)
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			h := hashFromName(e.Name())
			if h == "" || !strings.HasPrefix(h, hashPrefix) {
				continue
			}
			if full == "" {
				full = h
			} else if h != full {
				return "", false // prefix spans two distinct hashes: ambiguous
			}
			if best == "" || e.Name() > filepath.Base(best) {
				best = filepath.Join(dir, e.Name()) // lexically-greater basename == newer
			}
		}
	}
	if best == "" {
		return "", false // evicted or unknown
	}
	return best, true
}

func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}
