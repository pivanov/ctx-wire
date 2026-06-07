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
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"ctx-wire/internal/paths"
)

const (
	defaultMaxEntries = 50
	// perBodyCap bounds the emitted/raw text stored per entry, so one huge
	// command cannot blow the store.
	perBodyCap = 256 << 10
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
	// is acceptable). Rewrite atomically and privately.
	entries := readEntries(p)
	entries = append(entries, e)
	if max := opts.maxEntries(); len(entries) > max {
		entries = entries[len(entries)-max:]
	}
	writeEntries(p, entries)
}

// List returns the retained entries, oldest first. Best-effort: nil on error.
func List() []Entry {
	p, err := storePath()
	if err != nil {
		return nil
	}
	return readEntries(p)
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
	_ = os.Rename(tmpName, p)
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
