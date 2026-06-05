package install

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed pi_plugin.ts
var piPlugin string

//go:embed hermes_plugin.py
var hermesPluginInit string

//go:embed hermes_plugin.yaml
var hermesPluginManifest string

// PiPluginPath returns the Pi extension location, ~/.pi/agent/extensions/
// ctx-wire.ts (honoring PI_CODING_AGENT_DIR), mirroring rtk's layout.
func PiPluginPath() (string, error) {
	base := os.Getenv("PI_CODING_AGENT_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(base, "extensions", "ctx-wire.ts"), nil
}

// InstallPi writes the Pi extension (creating parent dirs); reports whether it
// changed. Idempotent.
func InstallPi(path string) (bool, error) {
	return writePluginFile(path, piPlugin)
}

// HermesPluginDir returns the Hermes plugin directory,
// ~/.hermes/plugins/ctx-wire-rewrite (honoring HERMES_HOME).
func HermesPluginDir() (string, error) {
	base := os.Getenv("HERMES_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".hermes")
	}
	return filepath.Join(base, "plugins", "ctx-wire-rewrite"), nil
}

// InstallHermes writes the Hermes plugin (__init__.py + plugin.yaml) into dir,
// creating it. Reports whether anything changed. Idempotent.
func InstallHermes(dir string) (bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	a, err := writePluginFile(filepath.Join(dir, "__init__.py"), hermesPluginInit)
	if err != nil {
		return false, err
	}
	b, err := writePluginFile(filepath.Join(dir, "plugin.yaml"), hermesPluginManifest)
	if err != nil {
		return false, err
	}
	return a || b, nil
}

// writePluginFile writes content to path (creating parent dirs), leaving an
// up-to-date file untouched. Reports whether the file changed.
func writePluginFile(path, content string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	existing, err := os.ReadFile(path)
	existed := err == nil
	if existed && string(existing) == content {
		return false, nil
	}
	return true, writeAtomic(path, []byte(content), existed)
}
