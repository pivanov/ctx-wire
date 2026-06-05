// Package paths centralizes where ctx-wire stores data and config, so the
// per-OS conventions (XDG on Unix, %LOCALAPPDATA%/%APPDATA% on Windows) live in
// one place. Both functions return the BASE directory; callers join the
// "ctx-wire" subfolder and a filename.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// DataHome returns the base data directory: $XDG_DATA_HOME, or %LOCALAPPDATA%
// on Windows, or ~/.local/share. The XDG variable wins on every OS so existing
// overrides and tests keep working.
func DataHome() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return d, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// ConfigHome returns the base config directory: $XDG_CONFIG_HOME, or %APPDATA%
// on Windows, or ~/.config.
func ConfigHome() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			return d, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// AppName is the per-base "ctx-wire" subfolder every path builder joins onto a
// base directory.
const AppName = "ctx-wire"

// OwnedDirs returns ctx-wire's dedicated config and data directories. Everything
// ctx-wire writes locally (config.toml, filters.toml, trust records, gain and
// shim logs, tee captures, telemetry config and state) lives inside these two
// folders, so removing them wholesale clears ctx-wire's entire local footprint.
// The order is stable (config first, then data) for predictable reporting.
func OwnedDirs() ([]string, error) {
	cfg, err := ConfigHome()
	if err != nil {
		return nil, err
	}
	data, err := DataHome()
	if err != nil {
		return nil, err
	}
	return []string{
		filepath.Join(cfg, AppName),
		filepath.Join(data, AppName),
	}, nil
}
