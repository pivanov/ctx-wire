package tee

// FetchStats is the aggregate fetch-redemption counter stored in
// DataHome/ctx-wire/fetch-stats.json. All fields are cumulative totals;
// there is no per-hash, per-path, or per-range breakdown (privacy-safe by design).
//
// Storage: a single JSON file, written atomically via a temp-then-rename.
// Reads and writes are lock-free; a lost update on a concurrent increment is
// acceptable because the counter is purely informational.

import (
	"encoding/json"
	"os"
	"path/filepath"

	"ctx-wire/internal/paths"
)

// FetchStats holds aggregate fetch redemption counts.
type FetchStats struct {
	Full          int64 `json:"full"`           // whole-output fetches
	Ranged        int64 `json:"ranged"`         // ranged (--lines) fetches
	Miss          int64 `json:"miss"`           // miss/evicted fetches
	BytesReturned int64 `json:"bytes_returned"` // bytes emitted by fetch (full + ranged)
	LinesReturned int64 `json:"lines_returned"` // lines emitted by ranged fetches
}

func fetchStatsPath() (string, error) {
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", "fetch-stats.json"), nil
}

// ReadFetchStats loads the aggregate counter from disk. If the file does not
// exist it returns a zero-value FetchStats with no error.
func ReadFetchStats() (FetchStats, error) {
	p, err := fetchStatsPath()
	if err != nil {
		return FetchStats{}, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return FetchStats{}, nil
	}
	if err != nil {
		return FetchStats{}, err
	}
	var s FetchStats
	if err := json.Unmarshal(data, &s); err != nil {
		return FetchStats{}, err
	}
	return s, nil
}

// IncrFetchStats applies delta to the stored aggregate counter. Errors are
// swallowed so a counter failure never breaks a fetch.
func IncrFetchStats(delta FetchStats) {
	p, err := fetchStatsPath()
	if err != nil {
		return
	}
	cur, _ := ReadFetchStats() // zero on any error
	cur.Full += delta.Full
	cur.Ranged += delta.Ranged
	cur.Miss += delta.Miss
	cur.BytesReturned += delta.BytesReturned
	cur.LinesReturned += delta.LinesReturned

	data, err := json.Marshal(cur)
	if err != nil {
		return
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	// Atomic write: write to a temp file in the same directory, then rename.
	tmp, err := os.CreateTemp(dir, "fetch-stats-*.json.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	_ = os.Rename(tmpName, p) // best-effort; a lost rename is an acceptable missed increment
}
