package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"ctx-wire/internal/paths"
)

// The deny loop-breaker. Hook invocations are one-shot processes, so "the
// agent is retrying a request we just denied" can only be detected through
// persisted state. Entries expire after denyTTL, which also rescues the
// Edit-precondition detour: deny -> shell read -> Edit refused -> the re-Read
// arrives within the TTL and is allowed through.
const denyTTL = 60 * time.Second

const denyStateName = "filetool-denies.json"

func denyStatePath() (string, error) {
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", denyStateName), nil
}

// recordDenyOnce reports whether a deny may be issued for this exact request.
// It returns false when the same request was denied within the TTL (the agent
// is retrying: let it through) or when the deny cannot be recorded (NEVER deny
// without recorded state, or the loop-breaker goes blind). On success the deny
// is recorded before the caller emits it.
func recordDenyOnce(sessionID, tool string, input []byte) bool {
	path, err := denyStatePath()
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(sessionID + "\x00" + tool + "\x00" + string(input)))
	key := hex.EncodeToString(sum[:])
	now := time.Now()

	entries := map[string]int64{}
	if data, rerr := os.ReadFile(path); rerr == nil {
		// Corrupt state starts fresh: worst case one extra redirect attempt.
		_ = json.Unmarshal(data, &entries)
	}
	for k, ts := range entries {
		if now.Sub(time.Unix(ts, 0)) > denyTTL {
			delete(entries, k)
		}
	}
	if ts, ok := entries[key]; ok && now.Sub(time.Unix(ts, 0)) <= denyTTL {
		return false // recently denied: the retry goes through
	}
	entries[key] = now.Unix()

	data, err := json.Marshal(entries)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return false
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false
	}
	return true
}
