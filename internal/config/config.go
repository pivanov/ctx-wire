// Package config loads ctx-wire's optional user config file. It is best-effort:
// a missing file is not an error, so ctx-wire works with no config at all.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"ctx-wire/internal/paths"
)

// envConfig overrides the config path (used by tests and unusual setups).
const envConfig = "CTX_WIRE_CONFIG"

// Config is the parsed user config (~/.config/ctx-wire/config.toml).
type Config struct {
	Hooks  Hooks  `toml:"hooks"`
	Output Output `toml:"output"`
}

// Output controls how filtered output is rendered.
type Output struct {
	// UltraCompact applies an extra compaction pass to filtered output (trims
	// trailing whitespace, collapses blank-line runs) for a few more tokens.
	UltraCompact bool `toml:"ultra_compact"`

	// MonthlyTokenBudget frames `gain --quota`: the tokens you aim to save (or
	// are allotted) per month. 0 leaves quota in its budget-free framing, where
	// savings are shown as context-window multiples. Deliberately vendor-neutral
	// (a token count you choose), not a fixed subscription tier.
	MonthlyTokenBudget int64 `toml:"monthly_token_budget"`

	// ContextWindow is the token size `gain --quota` frames savings against when
	// no budget is set ("saved N context windows"). 0 uses
	// gain.DefaultContextWindow.
	ContextWindow int64 `toml:"context_window"`
}

// Hooks holds command-rewrite policy.
type Hooks struct {
	// ExcludeCommands are command names the hook must never rewrite and the
	// runner must never filter (matched by basename, e.g. "curl"). For commands
	// whose raw output the agent needs verbatim.
	ExcludeCommands []string `toml:"exclude_commands"`

	// TransparentPrefixes are wrapper prefixes (e.g. "docker exec web") that the
	// hook peels before routing: the inner command is rewritten and the prefix
	// re-prepended, so `docker exec web git status` becomes
	// `docker exec web ctx-wire run git status`.
	TransparentPrefixes []string `toml:"transparent_prefixes"`
}

// Path returns the config file location: CTX_WIRE_CONFIG, else
// $XDG_CONFIG_HOME/ctx-wire/config.toml, else ~/.config/ctx-wire/config.toml.
func Path() (string, error) {
	if v := os.Getenv(envConfig); v != "" {
		return v, nil
	}
	base, err := paths.ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", "config.toml"), nil
}

// Load reads and parses the config file. A missing file yields a zero Config
// and no error; a malformed file returns a parse error.
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", p, err)
	}
	return c, nil
}
