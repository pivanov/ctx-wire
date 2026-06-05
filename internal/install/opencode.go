package install

import (
	_ "embed"
	"os"
	"path/filepath"

	"ctx-wire/internal/paths"
)

//go:embed opencode_plugin.ts
var opencodePlugin string

// OpenCodePluginPath returns the global OpenCode plugin location,
// ~/.config/opencode/plugins/ctx-wire.ts (honoring XDG_CONFIG_HOME). Mirrors
// rtk's proven layout.
func OpenCodePluginPath() (string, error) {
	base, err := paths.ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "opencode", "plugins", "ctx-wire.ts"), nil
}

// InstallOpenCode writes the OpenCode plugin (creating parent dirs) and reports
// whether the file changed. Idempotent: an up-to-date plugin is left untouched.
func InstallOpenCode(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	existing, err := os.ReadFile(path)
	existed := err == nil
	if existed && string(existing) == opencodePlugin {
		return false, nil
	}
	return true, writeAtomic(path, []byte(opencodePlugin), existed)
}
