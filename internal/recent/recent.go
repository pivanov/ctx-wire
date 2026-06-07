// Package recent stores a small, bounded, scrubbed record of recent `ctx-wire
// run` outputs so `ctx-wire inspect` can show raw-vs-filtered (and, later, dedup
// can detect unchanged output).
//
// This is a deliberate exception to the codebase's "do not persist successful
// output" stance, and it is constrained accordingly: off by default (opt-in),
// every field is already scrubbed by the caller before it arrives here, the
// store is strictly bounded by entry count, the file is private (0600), and the
// raw body is a separate opt-in tier. Disabled, every function here is a no-op.
package recent

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ctx-wire/internal/paths"
)

const (
	defaultMaxEntries = 50
	// perBodyCap bounds the emitted/raw text stored per entry, so one huge
	// command cannot blow the store.
	perBodyCap = 256 << 10
	// lockTTL is how long a lock file may sit before a peer treats it as
	// abandoned by a crashed writer and reclaims it.
	lockTTL = 10 * time.Second
)

// Options is the retention configuration (derived from config.Retention, then
// adjusted by ApplyEnv).
type Options struct {
	Enabled    bool
	RawBodies  bool // store the scrubbed raw body (the inspect audit tier)
	MaxEntries int
}

func (o Options) maxEntries() int {
	if o.MaxEntries > 0 {
		return o.MaxEntries
	}
	return defaultMaxEntries
}

// ApplyEnv lets a user disable retention for a single run without editing
// config, an escape hatch for a sensitive command: CTX_WIRE_RETENTION=0 turns
// the whole store off, CTX_WIRE_RETENTION_RAW=0 drops just the raw tier.
func ApplyEnv(o Options) Options {
	if envOff(os.Getenv("CTX_WIRE_RETENTION")) {
		o.Enabled = false
	}
	if envOff(os.Getenv("CTX_WIRE_RETENTION_RAW")) {
		o.RawBodies = false
	}
	return o
}

func envOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// Entry is one retained command output. Command, Emitted, and Raw are already
// scrubbed by the caller; nothing unscrubbed ever reaches disk.
type Entry struct {
	TS        string `json:"ts"`
	Command   string `json:"command"`
	Filter    string `json:"filter,omitempty"`
	Mode      string `json:"mode"`
	RawBytes  int    `json:"raw_bytes"`
	EmitBytes int    `json:"emit_bytes"`
	Exit      int    `json:"exit"`
	Hash      string `json:"hash"` // of the full emitted (scrubbed) output, pre-clip
	Emitted   string `json:"emitted"`
	Raw       string `json:"raw,omitempty"` // raw tier only
}

func storePath() (string, error) {
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, paths.AppName, "recent.jsonl"), nil
}

// Record appends an entry and trims the store to MaxEntries, best-effort. No-op
// when disabled. The Raw field is dropped unless RawBodies is on. Errors are
// swallowed: retention must never break a command.
func Record(opts Options, e Entry) {
	if !opts.Enabled {
		return
	}
	// Hash the FULL emitted before clipping, so the stored hash represents the
	// whole output the agent saw (robust for future dedup, no tail-difference
	// false positive when output exceeds the body cap).
	e.Hash = hashString(e.Emitted)
	e.Emitted = clip(e.Emitted)
	if opts.RawBodies {
		e.Raw = clip(e.Raw)
	} else {
		e.Raw = ""
	}

	p, err := storePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	// Read, append, and trim to MaxEntries on every record so the count bound is
	// always honored (the store is opt-in and bounded, so the read+rewrite cost
	// is acceptable). The read-modify-rewrite is NOT atomic across processes the
	// way an O_APPEND write was, so a cross-process lock serializes it: without
	// it, two concurrent `ctx-wire run` processes would lose each other's entry.
	unlock, ok := acquireLock(p)
	if !ok {
		return // contended or stale lock: skip this record (best-effort) rather than risk a racy rewrite
	}
	defer unlock()

	entries := readEntries(p)
	entries = append(entries, e)
	if max := opts.maxEntries(); len(entries) > max {
		entries = entries[len(entries)-max:]
	}
	writeEntries(p, entries)
}

// acquireLock serializes the read-modify-rewrite across processes with an
// O_EXCL lock file, capping the wait at ~200ms (retention is best-effort and on
// the command exit path, so skipping a record beats stalling output). A lock
// abandoned by a crashed writer is reclaimed once.
func acquireLock(p string) (func(), bool) {
	lockPath := p + ".lock"
	reclaimed := false
	for i := 0; i < 40; i++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, true
		}
		if !errors.Is(err, fs.ErrExist) {
			return func() {}, false
		}
		if !reclaimed && staleLock(lockPath) {
			reclaimed = true
			_ = os.Remove(lockPath)
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() {}, false
}

func staleLock(lockPath string) bool {
	info, err := os.Stat(lockPath)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > lockTTL
}

// List returns the retained entries, oldest first. Best-effort: nil on error.
func List() []Entry {
	p, err := storePath()
	if err != nil {
		return nil
	}
	return readEntries(p)
}

// Hash returns the content hash used for entries, so dedup can compare a fresh
// output against a stored entry's Hash with the same function.
func Hash(s string) string { return hashString(s) }

// LastMatch returns the most recent entry whose command equals command and whose
// timestamp is within `within` of now. ok is false when there is no such recent
// entry. Used by dedup: a match means the same command produced this output
// recently enough that the body is likely still in the agent's context.
func LastMatch(command string, within time.Duration, now time.Time) (Entry, bool) {
	entries := List()
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Command != command {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, e.TS)
		if err != nil {
			return Entry{}, false
		}
		if now.Sub(ts) <= within {
			return e, true
		}
		// The most recent match is already outside the window; older ones only
		// get further away, so stop.
		return Entry{}, false
	}
	return Entry{}, false
}

// readEntries decodes the JSONL store with a streaming decoder, so an entry of
// any size is read correctly (a fixed scanner buffer could silently drop a large
// entry, and every entry after it).
func readEntries(p string) []Entry {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Entry
	dec := json.NewDecoder(f)
	for {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			break
		}
		out = append(out, e)
	}
	return out
}

// writeEntries rewrites the store atomically and privately. os.CreateTemp makes
// the temp file 0600, so the final file (after rename) is private even though it
// may carry raw bodies.
func writeEntries(p string, entries []Entry) {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".recent-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	for _, e := range entries {
		if enc.Encode(e) != nil {
			tmp.Close()
			os.Remove(tmpName)
			return
		}
	}
	if tmp.Close() != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, p); err != nil {
		// Do not leave a private temp file (which may hold a raw body) behind on a
		// rare rename failure.
		os.Remove(tmpName)
	}
}

func clip(s string) string {
	if len(s) > perBodyCap {
		return s[:perBodyCap]
	}
	return s
}

func hashString(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}
