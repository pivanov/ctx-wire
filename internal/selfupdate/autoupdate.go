package selfupdate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"ctx-wire/internal/paths"
)

// autoUpdateArg is the hidden subcommand the foreground spawns to perform a
// detached, silent self-update. It is intentionally not in the public command
// list or help: it is an internal re-entry point, never typed by a person.
const autoUpdateArg = "__autoupdate"

// defaultInterval is the minimum time between background checks (~12x/day).
const defaultInterval = 2 * time.Hour

// claimLockMaxAge is a short stale-lock threshold for the foreground
// stamp+spawn window. The lock should normally live for milliseconds; anything
// older than this means the foreground process died between acquiring it and
// removing it.
const claimLockMaxAge = time.Minute

// EnvDisable turns background self-update off without editing config. Useful for
// CI, packagers, and tests.
const EnvDisable = "CTX_WIRE_NO_AUTOUPDATE"

// nowFunc is time.Now, a var so tests can pin the clock.
var nowFunc = time.Now

// spawnDetachedFunc is spawnDetached, a var so tests can prove the foreground
// scheduling behavior without launching a real updater.
var spawnDetachedFunc = spawnDetached

// checkState is the tiny update.json persisted in the data dir. It tracks only
// the last background check, kept separate from telemetry state so disabling
// telemetry never resets the auto-update throttle.
type checkState struct {
	LastCheck string `json:"last_check"`
}

func statePath() (string, error) {
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, paths.AppName, "update.json"), nil
}

func claimLockPath() (string, error) {
	p, err := statePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "update.lock"), nil
}

// readLastCheck returns the last background-check time, or the zero time when it
// has never run or the state is missing/unreadable (treated as "due").
func readLastCheck() time.Time {
	p, err := statePath()
	if err != nil {
		return time.Time{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}
	}
	var st checkState
	if json.Unmarshal(data, &st) != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, st.LastCheck)
	if err != nil {
		return time.Time{}
	}
	return t
}

// writeLastCheck records now as the last-check time (atomic temp+rename).
func writeLastCheck(now time.Time) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(checkState{LastCheck: now.UTC().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".ctx-wire-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

// due reports whether a check is owed: never checked, or older than interval.
func due(last, now time.Time, interval time.Duration) bool {
	if interval <= 0 {
		interval = defaultInterval
	}
	return last.IsZero() || now.Sub(last) >= interval
}

// acquireClaimLock wins the cross-process right to stamp the next update check
// and spawn the detached updater. It is intentionally filesystem-only because
// each ctx-wire invocation is a fresh process.
func acquireClaimLock(now time.Time) (func(), bool) {
	p, err := claimLockPath()
	if err != nil {
		return nil, false
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, false
	}
	open := func() (*os.File, error) {
		return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	f, err := open()
	if err != nil && os.IsExist(err) {
		if st, statErr := os.Stat(p); statErr == nil && now.Sub(st.ModTime()) > claimLockMaxAge {
			_ = os.Remove(p)
			f, err = open()
		}
	}
	if err != nil {
		return nil, false
	}
	_, _ = f.WriteString(now.UTC().Format(time.RFC3339Nano) + "\n")
	_ = f.Close()
	return func() { _ = os.Remove(p) }, true
}

// ShouldCheckOnCommand reports whether a foreground command may schedule a
// background update. The updater is cheap and detached, so every normal command
// can keep ctx-wire fresh; only explicit update/removal and the hidden updater
// itself are excluded.
func ShouldCheckOnCommand(cmd string) bool {
	switch cmd {
	case "", autoUpdateArg, "update", "uninstall":
		return false
	default:
		return true
	}
}

// MaybeBackgroundUpdate spawns a detached, silent self-update at most once per
// interval, then returns immediately. The download+install runs in a separate
// process, so it never blocks the caller or writes to the terminal. current is
// the running binary's version; interval<=0 uses the 2h default. Best-effort:
// any error (unparseable version, state I/O, spawn) is swallowed.
//
// It is safe to call from hot paths: when a check is not due, it performs only
// local state work; network and binary replacement happen only in the detached
// child after this process has returned.
func MaybeBackgroundUpdate(current string, interval time.Duration) {
	// Manual `ctx-wire update` works on Windows (zip support), but a silent
	// background swap of a running .exe is not yet validated on real Windows, and
	// a failed swap could leave a broken install. Keep background update off there
	// until replaceSelf is proven on Windows; manual update stays available.
	if runtime.GOOS == "windows" {
		return
	}
	if os.Getenv(EnvDisable) != "" {
		return
	}
	// Never auto-update a dev or otherwise unversioned build over itself.
	if _, _, ok := parseVersion(current); !ok {
		return
	}
	now := nowFunc()
	if !due(readLastCheck(), now, interval) {
		return
	}
	release, ok := acquireClaimLock(now)
	if !ok {
		return
	}
	defer release()
	// Another process may have won the race and stamped the check between our
	// first due check and the lock acquisition.
	if !due(readLastCheck(), now, interval) {
		return
	}
	// Stamp the check time before spawning so concurrent invocations don't all
	// spawn, and a persistently failing update doesn't retry on every command.
	if err := writeLastCheck(now); err != nil {
		return
	}
	spawnDetachedFunc()
}

// spawnDetached starts a background `ctx-wire __autoupdate` that performs the
// real update and exits. It is fully detached (own session/process group, no
// inherited stdio) so it outlives this short-lived CLI and never writes to the
// user's terminal.
func spawnDetached() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, autoUpdateArg)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	detach(cmd)
	if cmd.Start() != nil {
		return
	}
	// Do not Wait: let it outlive us. Release the handle so we leave no zombie.
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
}

// IsBackgroundArg reports whether arg invokes the hidden background-update
// subcommand, so main can dispatch it before any normal processing.
func IsBackgroundArg(arg string) bool {
	return arg == autoUpdateArg
}

// RunBackground performs a single silent update attempt for the detached child
// process. It never prints; failures are intentionally swallowed (best-effort).
func RunBackground(current string) {
	_, _ = Update(Options{Current: current})
}
