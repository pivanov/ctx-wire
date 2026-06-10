// Package config loads ctx-wire's optional user config file. It is best-effort:
// a missing file is not an error, so ctx-wire works with no config at all.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"ctx-wire/internal/paths"
)

// envConfig overrides the config path (used by tests and unusual setups).
const envConfig = "CTX_WIRE_CONFIG"

// Config is the parsed user config (~/.config/ctx-wire/config.toml).
type Config struct {
	Hooks     Hooks     `toml:"hooks"`
	Output    Output    `toml:"output"`
	Update    Update    `toml:"update"`
	Retention Retention `toml:"retention"`
	Dedup     Dedup     `toml:"dedup"`
}

// Dedup controls repeat-command dedup: when a read-only command re-runs with
// byte-identical output, ctx-wire emits a short recoverable reference instead of
// the body. Off by default. The command still runs; only the re-emission is
// saved.
type Dedup struct {
	// Enabled turns dedup on. It implies the recent-outputs store is recording
	// (so a reference can be compared and recovered via inspect).
	Enabled bool `toml:"enabled"`

	// RecencyMinutes bounds how recent a prior run must be to dedup against it,
	// the dead-pointer mitigation (default 60). A reference is only emitted when
	// the unchanged body is likely still in the agent's context.
	RecencyMinutes int `toml:"recency_minutes"`
}

// Recency returns the configured dedup recency window, or the 60-minute default.
func (d Dedup) Recency() time.Duration {
	if d.RecencyMinutes <= 0 {
		return 60 * time.Minute
	}
	return time.Duration(d.RecencyMinutes) * time.Minute
}

// Retention controls the recent-outputs store that powers `ctx-wire inspect`
// (and, later, dedup). It is a deliberate exception to "do not persist
// successful output", so it is OFF unless explicitly enabled.
type Retention struct {
	// Enabled turns the store on. Unset or false means off (the default): no
	// successful-command output is persisted.
	Enabled bool `toml:"enabled"`

	// RawBodies also stores the scrubbed raw (pre-filter) body, which `inspect`
	// needs for a full raw-vs-filtered audit. Off by default; this is the larger
	// persistence cost, paid only when the user wants the audit trail.
	RawBodies bool `toml:"raw_bodies"`

	// MaxEntries caps how many recent entries are kept (0 uses the default).
	MaxEntries int `toml:"max_entries"`
}

// Update controls background self-update behavior.
type Update struct {
	// Auto enables periodic background self-update checks (about every 2 hours,
	// only on human-facing commands, never on the run/hook hot path). When a
	// newer release is found it is downloaded, checksum-verified, and atomically
	// installed by a detached background process. Unset means enabled; set
	// `auto = false` to turn it off. The CTX_WIRE_NO_AUTOUPDATE env var also
	// disables it.
	Auto *bool `toml:"auto"`

	// IntervalHours overrides the minimum hours between checks (default 2).
	IntervalHours int `toml:"interval_hours"`
}

// AutoEnabled reports whether background self-update is on (the default).
func (u Update) AutoEnabled() bool {
	return u.Auto == nil || *u.Auto
}

// Interval returns the configured minimum time between checks, or 0 to let the
// selfupdate package apply its default.
func (u Update) Interval() time.Duration {
	if u.IntervalHours <= 0 {
		return 0
	}
	return time.Duration(u.IntervalHours) * time.Hour
}

// Output controls how filtered output is rendered.
type Output struct {
	// UltraCompact applies an extra compaction pass to filtered output (trims
	// trailing whitespace, collapses blank-line runs) for a few more tokens.
	UltraCompact bool `toml:"ultra_compact"`

	// Truncate scales every filter's numeric caps (truncate_lines_at,
	// head/tail, max_lines, group caps) without editing TOML:
	// "light" doubles the caps, "aggressive" halves them (floor 1), "none"
	// removes them, "default"/empty applies them as written. Filters still only
	// act on output they positively recognize; the dial never widens what gets
	// filtered, only how much of it is kept. CTX_WIRE_TRUNCATE overrides per
	// invocation.
	Truncate string `toml:"truncate"`

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

	// CaptureFileTools is the file-tools capture experiment (Claude only,
	// default off): the PreToolUse hook denies built-in Read/Grep calls it can
	// translate exactly, suggesting the equivalent filtered shell command.
	// Toggle with `ctx-wire init claude --capture-files|--no-capture-files`.
	CaptureFileTools bool `toml:"capture_file_tools"`
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
