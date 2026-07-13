// Package gain records local per-command token-savings data to a JSONL file and
// summarizes it. Storage is pure Go (append-only JSONL, no CGO). Only byte
// counts and a scrubbed command string are stored: raw argv and output never
// touch the log. Recording is best-effort and must never break a command.
package gain

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ctx-wire/internal/paths"
	"ctx-wire/internal/scrub"
)

// gainLockTTL bounds how long a gain lock may persist before it is treated as
// abandoned. A healthy writer holds the lock for well under a millisecond, so
// any lock older than this was left behind by a crashed or killed process. The
// age backstop also covers PID reuse, where the recorded PID now names an
// unrelated live process.
const gainLockTTL = 30 * time.Second

const (
	envDisable      = "CTX_WIRE_GAIN"               // set to "0" to disable recording
	envFile         = "CTX_WIRE_GAIN_FILE"          // override the JSONL path (tests)
	envFallbackFile = "CTX_WIRE_GAIN_FALLBACK_FILE" // override fallback path (tests)
)

const maxGainRotations = 5

var maxGainLogSize int64 = 8 << 20 // 8 MiB

const (
	maxCommandSampleBytes = 16 << 10
	maxGainLineBytes      = 1 << 20
)

var gainWriteMu sync.Mutex

// scrubFailClosed is a seam so the fail-closed (entry-withheld) branch is testable.
var scrubFailClosed = scrub.ScrubFailClosed

// Enabled reports whether gain recording is enabled for this process.
func Enabled() bool {
	return os.Getenv(envDisable) != "0"
}

// Entry is one recorded command execution.
type Entry struct {
	TS           string `json:"ts"`
	Command      string `json:"command"` // scrubbed
	Filter       string `json:"filter,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Agent        string `json:"agent,omitempty"`  // invoking agent, "" when unattributed
	Source       string `json:"source,omitempty"` // "hook" | "shim" | "run" | "mcp": how ctx-wire was reached
	RawBytes     int    `json:"raw_bytes"`
	EmittedBytes int    `json:"emitted_bytes"`
	SavedBytes   int    `json:"saved_bytes"`
	ExitCode     int    `json:"exit_code"`
}

func gainPath() (string, error) {
	if f := os.Getenv(envFile); f != "" {
		return f, nil
	}
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", "gain.jsonl"), nil
}

func fallbackGainPath() (string, error) {
	if f := os.Getenv(envFallbackFile); f != "" {
		return f, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "unknown"
	}
	sum := sha256.Sum256([]byte(home))
	dir := fmt.Sprintf("ctx-wire-%x", sum[:4])
	return filepath.Join(os.TempDir(), dir, "gain.jsonl"), nil
}

func gainBasePaths() ([]string, error) {
	path, err := gainPath()
	if err != nil {
		return nil, err
	}
	if os.Getenv(envFile) != "" {
		return []string{path}, nil
	}
	fallback, err := fallbackGainPath()
	if err != nil {
		return nil, err
	}
	if fallback == path {
		return []string{path}, nil
	}
	return []string{path, fallback}, nil
}

func gainReadPaths() ([]string, error) {
	base, err := gainBasePaths()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range base {
		paths = append(paths, gainLogFamily(path)...)
	}
	return paths, nil
}

func gainLogFamily(path string) []string {
	paths := make([]string, 0, maxGainRotations+1)
	for i := maxGainRotations; i >= 1; i-- {
		paths = append(paths, fmt.Sprintf("%s.%d", path, i))
	}
	paths = append(paths, path)
	return paths
}

// PrimaryPath returns the primary gain log path for the current environment.
// Exposed read-only for diagnostics (ctx-wire doctor).
func PrimaryPath() (string, error) {
	return gainPath()
}

// WriteDirs returns the candidate gain-log directories in the same priority
// order Record uses: primary first, then fallback (omitted when an explicit
// CTX_WIRE_GAIN_FILE override is set, which disables the fallback). Exposed
// read-only for diagnostics.
func WriteDirs() ([]string, error) {
	paths, err := gainBasePaths()
	if err != nil {
		return nil, err
	}
	dirs := make([]string, len(paths))
	for i, p := range paths {
		dirs[i] = filepath.Dir(p)
	}
	return dirs, nil
}

// RecentEntries returns up to n most-recent recorded entries (newest last),
// merged across the primary and fallback logs. Commands are already scrubbed at
// record time. Used by diagnostics; read-only.
func RecentEntries(n int) ([]Entry, error) {
	if n <= 0 {
		return nil, nil
	}
	paths, err := gainReadPaths()
	if err != nil {
		return nil, err
	}
	var all []Entry
	for _, path := range paths {
		entries, err := readEntries(path)
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		ti, ei := time.Parse(time.RFC3339, all[i].TS)
		tj, ej := time.Parse(time.RFC3339, all[j].TS)
		if ei != nil || ej != nil {
			return i < j
		}
		return ti.Before(tj)
	})
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// readEntries parses all valid JSONL entries from path. A missing file yields no
// entries and no error; malformed lines are skipped.
func readEntries(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	if err := scanGainLines(f, func(line []byte) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		entries = append(entries, e)
	}); err != nil {
		return nil, err
	}
	return entries, nil
}

// Record appends a gain entry. The command is scrubbed before storage, so
// secrets in argv never reach the log. Disabled by CTX_WIRE_GAIN=0.
func Record(command string, rawBytes, emittedBytes, exitCode int) error {
	return RecordWithMeta(command, "", "", "", "", rawBytes, emittedBytes, exitCode)
}

// RecordMCP records a gain entry for the MCP surface: source="mcp". The MCP
// server's run_command/read_file tools and the `mcp-wrap --compress` relay all
// reduce output but are reached via MCP, not the shell hook, so they belong on
// the source axis as a 4th reach-path (parallel to hook/shim/run); source always
// coincides with the MCP surface here. agentName is the caller's agent.Current()
// (gain stays decoupled from agent). Best-effort; never breaks a run.
func RecordMCP(command, filterName, mode, agentName string, rawBytes, emittedBytes int) {
	_ = RecordWithMeta(command, filterName, mode, agentName, "mcp", rawBytes, emittedBytes, 0)
}

// RecordWithMeta appends a gain entry with filter-path metadata. filterName is
// the matched filter, when any. mode is usually "filtered" or "passthrough".
// agentName attributes the command to the invoking agent (already normalized;
// "" when unattributed). source is how ctx-wire was reached ("hook" | "shim" |
// "run" | "mcp"), so entry-point savings can be compared.
func RecordWithMeta(command, filterName, mode, agentName, source string, rawBytes, emittedBytes, exitCode int) error {
	if !Enabled() {
		return nil
	}
	path, err := gainPath()
	if err != nil {
		return err
	}
	// A synthetic on_empty message can make emitted exceed raw; never record
	// negative savings.
	saved := rawBytes - emittedBytes
	if saved < 0 {
		saved = 0
	}
	cmd, ok1 := scrubFailClosed(command)
	fname, ok2 := scrubFailClosed(filterName)
	md, ok3 := scrubFailClosed(mode)
	if !ok1 || !ok2 || !ok3 {
		return fmt.Errorf("gain: scrub failed closed, entry withheld")
	}
	line, err := json.Marshal(Entry{
		TS:           time.Now().UTC().Format(time.RFC3339),
		Command:      truncateCommandSample(cmd),
		Filter:       fname,
		Mode:         md,
		Agent:        agentName,
		Source:       source,
		RawBytes:     rawBytes,
		EmittedBytes: emittedBytes,
		SavedBytes:   saved,
		ExitCode:     exitCode,
	})
	if err != nil {
		return err
	}

	if err := appendLine(path, line); err == nil || os.Getenv(envFile) != "" {
		if err == nil && os.Getenv(envFile) == "" {
			_ = drainFallbackGain(path)
		}
		return err
	}
	fallback, err := fallbackGainPath()
	if err != nil {
		return err
	}
	return appendLine(fallback, line)
}

func appendLine(path string, line []byte) error {
	data := make([]byte, 0, len(line)+1)
	data = append(data, line...)
	data = append(data, '\n')
	return appendBytes(path, data)
}

func appendBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	gainWriteMu.Lock()
	defer gainWriteMu.Unlock()
	unlock, err := acquireGainLock(path)
	if err != nil {
		return err
	}
	defer unlock()
	return appendBytesLocked(path, data)
}

func appendBytesLocked(path string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := rotateGainLog(path, int64(len(data))); err != nil {
		return err
	}
	// O_APPEND keeps concurrent writes from multiple ctx-wire processes
	// interleaving; JSONL lines are small enough for atomic appends.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// drainFallbackGain moves entries from the sandbox fallback log family into the
// durable primary log after a primary write has succeeded. This lets sandboxed
// Codex/Desktop-App writes survive once any later unsandboxed ctx-wire command
// can write the primary store. Best-effort: failures leave the fallback intact
// for the next successful primary write.
func drainFallbackGain(primary string) error {
	fallback, err := fallbackGainPath()
	if err != nil {
		return err
	}
	if fallback == primary {
		return nil
	}
	if !fallbackFamilyHasData(fallback) {
		return nil
	}

	gainWriteMu.Lock()
	defer gainWriteMu.Unlock()

	// Lock the current fallback path; fallback writers also hold this lock while
	// rotating the family, so this serializes the read+clear with concurrent
	// sandboxed appends.
	unlockFallback, err := acquireGainLock(fallback)
	if err != nil {
		return err
	}
	defer unlockFallback()

	var drained bytes.Buffer
	fallbackPaths := gainLogFamily(fallback)
	for _, path := range fallbackPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		if len(data) == 0 {
			continue
		}
		drained.Write(data)
		if data[len(data)-1] != '\n' {
			drained.WriteByte('\n')
		}
	}
	if drained.Len() == 0 {
		return nil
	}

	unlockPrimary, err := acquireGainLock(primary)
	if err != nil {
		return err
	}
	if err := appendBytesLocked(primary, drained.Bytes()); err != nil {
		unlockPrimary()
		return err
	}
	unlockPrimary()

	var firstErr error
	for _, path := range fallbackPaths {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func fallbackFamilyHasData(fallback string) bool {
	for _, path := range gainLogFamily(fallback) {
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			return true
		}
	}
	return false
}

func truncateCommandSample(s string) string {
	if len(s) <= maxCommandSampleBytes {
		return s
	}
	const suffix = " [truncated]"
	limit := maxCommandSampleBytes - len(suffix)
	if limit < 0 {
		limit = maxCommandSampleBytes
	}
	return s[:limit] + suffix
}

func scanGainLines(f *os.File, fn func([]byte)) error {
	r := bufio.NewReader(f)
	var line []byte
	oversize := false
	for {
		frag, err := r.ReadSlice('\n')
		if len(frag) > 0 {
			if !oversize && len(line)+len(frag) <= maxGainLineBytes {
				line = append(line, frag...)
			} else {
				oversize = true
				line = nil
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if !oversize && len(line) > 0 {
			fn(line)
		}
		line = nil
		oversize = false
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func acquireGainLock(path string) (func(), error) {
	lockPath := path + ".lock"
	reclaimed := false
	// Cap the wait at ~200ms (40 * 5ms). Gain recording is best-effort and sits
	// on the command's exit path, so under heavy lock contention it is better to
	// skip a record than to stall the user's output for up to a second.
	for i := 0; i < 40; i++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		// Reclaim a lock abandoned by a crashed writer so gain logging never
		// freezes permanently (the whole reason a stale .lock was poisoning
		// every write). Steal at most once per call so two live peers cannot
		// both decide the other is stale and clobber each other.
		if !reclaimed && staleGainLock(lockPath) {
			reclaimed = true
			_ = os.Remove(lockPath)
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil, fmt.Errorf("gain log lock busy: %s", lockPath)
}

// staleGainLock reports whether the lock file looks abandoned: it is older than
// gainLockTTL, or its recorded PID is no longer running. A healthy writer holds
// the lock far too briefly for either to match, so this never reclaims a lock
// from a live peer mid-write.
func staleGainLock(lockPath string) bool {
	info, err := os.Stat(lockPath)
	if err != nil {
		// Already gone; the next O_EXCL create will win on its own.
		return false
	}
	if time.Since(info.ModTime()) > gainLockTTL {
		return true
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		// No usable PID and within the TTL window: leave it for the age check.
		return false
	}
	return !processAlive(pid)
}

func rotateGainLog(path string, incoming int64) error {
	if maxGainLogSize <= 0 {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if st.Size()+incoming <= maxGainLogSize {
		return nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", path, maxGainRotations))
	for i := maxGainRotations - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", path, i)
		next := fmt.Sprintf("%s.%d", path, i+1)
		if err := os.Rename(old, next); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(path, fmt.Sprintf("%s.1", path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Clear removes the gain logs used by the current environment. Missing logs are
// ignored.
func Clear() error {
	paths, err := gainReadPaths()
	if err != nil {
		return err
	}
	var firstErr error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
