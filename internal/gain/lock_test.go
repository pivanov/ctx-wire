package gain

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// deadPID returns a PID no live process can own: 0x7fffffff sits above the
// default pid_max on Linux and macOS, so a liveness probe always reports gone.
func deadPID(t *testing.T) int {
	t.Helper()
	return 0x7fffffff
}

func writeLock(t *testing.T, lockPath, body string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(lockPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if age > 0 {
		when := time.Now().Add(-age)
		if err := os.Chtimes(lockPath, when, when); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
}

func TestStaleGainLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "gain.jsonl.lock")

	t.Run("dead pid is stale", func(t *testing.T) {
		writeLock(t, lockPath, strconv.Itoa(deadPID(t))+"\n", 0)
		if !staleGainLock(lockPath) {
			t.Fatal("dead-PID lock should be stale")
		}
	})

	t.Run("live pid is not stale", func(t *testing.T) {
		writeLock(t, lockPath, strconv.Itoa(os.Getpid())+"\n", 0)
		if staleGainLock(lockPath) {
			t.Fatal("lock owned by this live process must not be stale")
		}
	})

	t.Run("old lock is stale regardless of pid", func(t *testing.T) {
		// Own (live) PID, but older than the TTL: covers PID reuse.
		writeLock(t, lockPath, strconv.Itoa(os.Getpid())+"\n", gainLockTTL+time.Second)
		if !staleGainLock(lockPath) {
			t.Fatal("lock older than TTL should be stale")
		}
	})

	t.Run("unreadable pid within ttl is not stale", func(t *testing.T) {
		writeLock(t, lockPath, "not-a-pid\n", 0)
		if staleGainLock(lockPath) {
			t.Fatal("fresh lock with no usable PID should wait for the age backstop")
		}
	})

	t.Run("missing lock is not stale", func(t *testing.T) {
		_ = os.Remove(lockPath)
		if staleGainLock(lockPath) {
			t.Fatal("absent lock should not be reported stale")
		}
	})
}

func TestAcquireGainLockReclaimsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gain.jsonl")
	lockPath := path + ".lock"

	// Poison the lock the way a crashed writer would: a dead PID that is never
	// cleaned up. Before the fix this made acquireGainLock fail forever.
	writeLock(t, lockPath, strconv.Itoa(deadPID(t))+"\n", 0)

	unlock, err := acquireGainLock(path)
	if err != nil {
		t.Fatalf("acquireGainLock should reclaim a stale lock, got: %v", err)
	}
	defer unlock()

	// The lock now belongs to us: it should carry our PID.
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read reclaimed lock: %v", err)
	}
	if got := string(data); got != strconv.Itoa(os.Getpid())+"\n" {
		t.Errorf("reclaimed lock body = %q, want our PID", got)
	}
}
