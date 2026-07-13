package selfupdate

// Post-update migrations. A self-update (or any out-of-band binary swap) only
// replaces the executable; the newly installed binary still has to *run* to
// apply any config migrations its version introduced. MaybeRunPostUpdate fires
// those migrations once, on the first real command after the version changes, so
// existing installs pick up config-level fixes (e.g. the Codex sandbox
// writable-root grant) without the user re-running `init`.

import (
	"os"
	"path/filepath"
	"strings"

	"ctx-wire/internal/paths"
)

// syncedVersionPath is a marker recording the version whose post-update
// migrations have already run, kept next to update.json in the data dir.
func syncedVersionPath() (string, error) {
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, paths.AppName, "synced-version"), nil
}

func readSyncedVersion() string {
	p, err := syncedVersionPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeSyncedVersion(v string) error {
	p, err := syncedVersionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(v+"\n"), 0o644)
}

// MaybeRunPostUpdate runs migrate after the running binary's version changes and
// records that version only when migrate reports the outcome is settled, so a
// migration blocked by a transient failure (e.g. a codex sandbox denying the
// write) retries on the next command instead of being falsely marked done.
// Dev/unversioned builds are skipped (they would fire every run). Best-effort and
// silent; migrate must be idempotent. Called from the normal command path so it
// runs on the first real invocation of a freshly installed binary.
func MaybeRunPostUpdate(current string, migrate func() (settled bool)) {
	if _, _, ok := parseVersion(current); !ok {
		return
	}
	if readSyncedVersion() == current {
		return
	}
	if runMigrateGuarded(migrate) {
		_ = writeSyncedVersion(current)
	}
}

// runMigrateGuarded runs migrate and turns a panic into a non-settled result, so
// a bug in a migration can never abort the user's wrapped command and never
// records the version as done (it retries next run). Mirrors the fail-open guard
// the hook write path uses.
func runMigrateGuarded(migrate func() (settled bool)) (settled bool) {
	defer func() {
		if recover() != nil {
			settled = false
		}
	}()
	return migrate()
}
