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

// EnvDisable turns background self-update off without editing config. Useful for
// CI, packagers, and tests.
const EnvDisable = "CTX_WIRE_NO_AUTOUPDATE"

// nowFunc is time.Now, a var so tests can pin the clock.
var nowFunc = time.Now

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

// MaybeBackgroundUpdate spawns a detached, silent self-update at most once per
// interval, then returns immediately. The download+install runs in a separate
// process, so it never blocks the caller or writes to the terminal. current is
// the running binary's version; interval<=0 uses the 6h default. Best-effort:
// any error (unparseable version, state I/O, spawn) is swallowed.
//
// It is the caller's job to invoke this only on human-facing commands; it must
// never be wired into the run/hook/rewrite/mcp hot paths.
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
	// Stamp the check time before spawning so concurrent invocations don't all
	// spawn, and a persistently failing update doesn't retry on every command.
	if err := writeLastCheck(now); err != nil {
		return
	}
	spawnDetached()
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
