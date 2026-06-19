// Package telemetry reports aggregate, anonymous ctx-wire impact counters.
// It never sends commands, arguments, paths, output, repo names, hostnames,
// usernames, install IDs, or raw gain log rows.
package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ctx-wire/internal/gain"
	"ctx-wire/internal/paths"
)

const (
	DefaultEndpoint = "https://ctx-wire-telemetry.iweb-ivanov.workers.dev/v1/telemetry"

	envEnabled      = "CTX_WIRE_TELEMETRY"
	envImprovements = "CTX_WIRE_TELEMETRY_IMPROVEMENTS"
	envURL          = "CTX_WIRE_TELEMETRY_URL"
	envConfig       = "CTX_WIRE_TELEMETRY_CONFIG"
	envState        = "CTX_WIRE_TELEMETRY_STATE"

	requestTimeout = 900 * time.Millisecond

	autoFlushInterval         = 30 * time.Minute
	autoFlushCommandThreshold = int64(1000)
	autoFlushSavedThreshold   = int64(10 << 20)
)

var (
	sendPayload = postJSON
	clockNow    = time.Now
)

type Config struct {
	Enabled         *bool `json:"enabled,omitempty"`
	InstallReported bool  `json:"install_reported,omitempty"`
	// PreviewShown latches the one-time consent invite shown on the first
	// interactive `ctx-wire gain`, so it is shown once and never repeated. The
	// invite uses a fixed mock payload; the real payload is shown only by the
	// explicit `ctx-wire telemetry preview` command.
	PreviewShown bool `json:"preview_shown,omitempty"`
	// InstalledAgents is the set of agents this config has ever reported
	// configuring (claude, codex, ...). It is informational only: every
	// successful `ctx-wire init <agent>` still reports an install event.
	// Agent type is a category, not an identity, so it stays within the
	// anonymous, aggregate-only model.
	InstalledAgents []string `json:"installed_agents,omitempty"`
	// ShareImprovements gates ONLY the granular per-program ("help improve
	// ctx-wire") breakdown. nil means "never decided" and defaults to on; an
	// explicit false keeps the website stats (totals, agents, country, install)
	// flowing while withholding the per-command detail. The master Enabled switch
	// gates everything; this is the finer, second toggle.
	ShareImprovements *bool `json:"share_improvements,omitempty"`
	// MigrationNoticeShown latches the one-time, non-interactive opt-out migration
	// notice. It is SEPARATE from PreviewShown on purpose: a swallowed
	// non-interactive notice must not suppress the reliable interactive one, so the
	// worst case is "shown twice", never "shown zero times".
	MigrationNoticeShown bool `json:"migration_notice_shown,omitempty"`
}

type Status struct {
	Enabled           bool
	ForcedByEnv       bool
	Endpoint          string
	ConfigPath        string
	StatePath         string
	InstallReported   bool
	ShareImprovements bool
}

type Result struct {
	Disabled        bool
	Noop            bool
	Sent            bool
	InstallReported bool
	// MachineFirst is true when this config reported an install for the first
	// time (used to show the one-time telemetry notice). AgentReported is true
	// when this call sent an agent-attributed install event.
	MachineFirst  bool
	AgentReported bool
}

type Totals struct {
	Commands     int64                    `json:"commands"`
	RawBytes     int64                    `json:"raw_bytes"`
	EmittedBytes int64                    `json:"emitted_bytes"`
	BytesSaved   int64                    `json:"bytes_saved"`
	TokensSaved  int64                    `json:"tokens_saved"`
	Programs     map[string]ProgramTotals `json:"programs,omitempty"`
	// Agents is the same token-only breakdown bucketed by the invoking agent
	// (claude, codex, ...). The agent type is a category, not an identity, so it
	// fits the existing anonymous, aggregate-only model. Unattributed commands are
	// omitted here; the site derives that bucket from Commands minus the agent
	// sum. Dollar figures are never sent: pricing lives on the website.
	Agents map[string]ProgramTotals `json:"agents,omitempty"`
}

type ProgramTotals struct {
	Count        int64 `json:"count"`
	RawBytes     int64 `json:"raw_bytes"`
	EmittedBytes int64 `json:"emitted_bytes"`
	BytesSaved   int64 `json:"bytes_saved"`
	TokensSaved  int64 `json:"tokens_saved"`
}

type stateFile struct {
	LastReported Totals `json:"last_reported"`
	Pending      Totals `json:"pending,omitempty"`
	LastAttempt  string `json:"last_attempt,omitempty"`
}

type impactPayload struct {
	Schema       int                      `json:"schema"`
	Event        string                   `json:"event"`
	Version      string                   `json:"version,omitempty"`
	Commands     int64                    `json:"commands,omitempty"`
	RawBytes     int64                    `json:"raw_bytes,omitempty"`
	EmittedBytes int64                    `json:"emitted_bytes,omitempty"`
	BytesSaved   int64                    `json:"bytes_saved,omitempty"`
	TokensSaved  int64                    `json:"tokens_saved,omitempty"`
	Programs     map[string]ProgramTotals `json:"programs,omitempty"`
	Agents       map[string]ProgramTotals `json:"agents,omitempty"`
}

type installPayload struct {
	Schema  int    `json:"schema"`
	Event   string `json:"event"`
	Version string `json:"version,omitempty"`
	// Agent is the configured agent (claude, codex, ...). Machine requests that
	// the server increments the aggregate reported-install counter; every
	// successful `ctx-wire init <agent>` sends it.
	Agent   string `json:"agent,omitempty"`
	Machine bool   `json:"machine,omitempty"`
}

func GetStatus() (Status, error) {
	cfg, err := readConfig()
	if err != nil {
		return Status{}, err
	}
	configPath, err := configPath()
	if err != nil {
		return Status{}, err
	}
	statePath, err := statePath()
	if err != nil {
		return Status{}, err
	}
	enabled, forced := enabled(cfg)
	return Status{
		Enabled:           enabled,
		ForcedByEnv:       forced,
		Endpoint:          endpoint(),
		ConfigPath:        configPath,
		StatePath:         statePath,
		InstallReported:   cfg.InstallReported,
		ShareImprovements: shareImprovements(cfg),
	}, nil
}

func SetEnabled(value bool) error {
	cfg, err := readConfig()
	if err != nil {
		return err
	}
	cfg.Enabled = &value
	if !value {
		_ = ClearState()
	}
	return writeConfig(cfg)
}

// shareImprovements reports whether the granular per-program ("help improve
// ctx-wire") breakdown may ride along. It is the second, finer toggle: callers
// gate the whole send on enabled() first, and this only decides whether the
// per-command detail is included. Defaults ON; opting out is an explicit choice.
func shareImprovements(cfg Config) bool {
	if v := strings.TrimSpace(strings.ToLower(os.Getenv(envImprovements))); v != "" {
		switch v {
		case "0", "false", "off", "no":
			return false
		case "1", "true", "on", "yes":
			return true
		}
	}
	if cfg.ShareImprovements != nil {
		return *cfg.ShareImprovements
	}
	return true
}

// SetShareImprovements records the user's choice for the per-program improvement
// data. The master Enabled switch is untouched, so the website stats keep
// flowing; this only adds or removes the granular per-command breakdown.
func SetShareImprovements(value bool) error {
	cfg, err := readConfig()
	if err != nil {
		return err
	}
	cfg.ShareImprovements = &value
	if err := writeConfig(cfg); err != nil {
		return err
	}
	if !value {
		// Absorb any per-command detail already pending into the LastReported mark
		// (accounted, never sent) and drop it from pending. This stops the pending
		// detail from shipping AND keeps the gain-log delta correct after a later
		// re-enable. Best-effort: a state error is not worth failing the toggle.
		if st, err := readState(); err == nil && len(st.Pending.Programs) > 0 {
			st.LastReported.Programs = mergeBuckets(st.LastReported.Programs, st.Pending.Programs)
			st.Pending.Programs = nil
			_ = writeState(st)
		}
	}
	return nil
}

func ClearState() error {
	path, err := statePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Forget withdraws telemetry consent and erases all local telemetry data: the
// pending/last-reported counters and the install-reported flag. ctx-wire
// telemetry carries no device identity and only ever sends aggregate counters,
// so there is nothing to erase server-side; this is the local equivalent of
// withdrawal plus erasure.
//
// Telemetry is opt-out (on by default), so withdrawal is a deliberate "no":
// Forget persists {enabled: false} rather than deleting the config. That records
// an explicit choice, distinct from "never decided" (nil) which an update
// migrates to on, so a withdrawal sticks: the one-time notice does not re-appear
// and no update reverses it. Missing files are not an error.
func Forget() error {
	if err := ClearState(); err != nil {
		return err
	}
	disabled := false
	return writeConfig(Config{Enabled: &disabled})
}

// ReportInstall records a successful init. agentName is the configured agent
// (claude, codex, ...). Every successful agent init reports an install event,
// including repeats for the same agent. The local config only latches the
// one-time telemetry notice and tracks which agent names have been seen before.
// It sends nothing when telemetry is disabled.
func ReportInstall(agentName string) (Result, error) {
	cfg, err := readConfig()
	if err != nil {
		return Result{}, err
	}
	isEnabled, _ := enabled(cfg)
	if !isEnabled {
		return Result{Disabled: true}, nil
	}

	ag := safeAgentName(agentName)
	machineFirst := !cfg.InstallReported
	if ag == "" && !machineFirst {
		return Result{Noop: true, InstallReported: true}, nil
	}

	if err := sendPayload(installPayload{Schema: 1, Event: "install", Version: buildVersion, Agent: ag, Machine: true}); err != nil {
		return Result{}, err
	}
	cfg.InstallReported = true
	agentNew := ag != "" && !containsAgent(cfg.InstalledAgents, ag)
	if agentNew {
		cfg.InstalledAgents = append(cfg.InstalledAgents, ag)
	}
	if err := writeConfig(cfg); err != nil {
		return Result{}, err
	}
	return Result{Sent: true, InstallReported: true, MachineFirst: machineFirst, AgentReported: ag != ""}, nil
}

func containsAgent(list []string, name string) bool {
	for _, a := range list {
		if a == name {
			return true
		}
	}
	return false
}

// buildImpactPayload constructs the wire payload from a totals delta and applies
// the improvements sub-toggle in ONE place: when sharing is off, the per-command
// Programs breakdown is withheld while the website stats (totals, agents) still
// flow. EVERY send path (the manual `gain` flush, the hook auto-flush, the
// preview) builds through here, so the gate can never be missed by one of them.
func buildImpactPayload(delta Totals, cfg Config) impactPayload {
	p := impactPayload{
		Schema:       1,
		Event:        "impact",
		Version:      buildVersion,
		Commands:     delta.Commands,
		RawBytes:     delta.RawBytes,
		EmittedBytes: delta.EmittedBytes,
		BytesSaved:   delta.BytesSaved,
		TokensSaved:  delta.TokensSaved,
		Programs:     delta.Programs,
		Agents:       delta.Agents,
	}
	if !shareImprovements(cfg) {
		p.Programs = nil
	}
	return p
}

func ReportImpact(summary *gain.Summary) (Result, error) {
	cfg, err := readConfig()
	if err != nil {
		return Result{}, err
	}
	isEnabled, _ := enabled(cfg)
	if !isEnabled {
		return Result{Disabled: true}, nil
	}
	// Backfill the install if it was never reported: init may have run while
	// telemetry was off (or before opt-out), then telemetry came on. Count it from
	// the first impact flush. Best-effort; never blocks the impact report.
	if !cfg.InstallReported {
		_, _ = ReportInstall("")
	}
	if summary == nil || summary.Commands == 0 {
		return Result{Noop: true}, nil
	}
	current := totalsFromSummary(summary)
	st, err := readState()
	if err != nil {
		return Result{}, err
	}
	delta := subtractTotals(current, st.LastReported)
	if totalsEmpty(delta) {
		return Result{Noop: true}, nil
	}
	sanitizeTotals(&delta) // belt-and-suspenders: nothing unsafe reaches the wire

	payload := buildImpactPayload(delta, cfg)
	if err := sendPayload(payload); err != nil {
		return Result{}, err
	}
	st.LastReported = current
	st.Pending = Totals{}
	st.LastAttempt = clockNow().UTC().Format(time.RFC3339)
	if err := writeState(st); err != nil {
		return Result{}, err
	}
	return Result{Sent: true}, nil
}

// RecordCommand adds one command's aggregate impact to the local pending
// telemetry accumulator and flushes only when the interval/volume thresholds are
// met. It never sends command text: command is used only to derive the same
// program bucket shown by `ctx-wire gain`. agentName attributes the command to
// the invoking agent (already normalized; "" when unattributed).
func RecordCommand(command, agentName string, rawBytes, emittedBytes int) (Result, error) {
	cfg, err := readConfig()
	if err != nil {
		return Result{}, err
	}
	isEnabled, _ := enabled(cfg)
	if !isEnabled {
		return Result{Disabled: true}, nil
	}

	now := clockNow().UTC()
	st, err := readState()
	if err != nil {
		return Result{}, err
	}
	rec := totalsFromCommand(command, agentName, rawBytes, emittedBytes)
	if !shareImprovements(cfg) {
		// Sharing is off: drop per-command detail from pending AND advance the
		// LastReported high-water mark by it. Advancing the mark keeps it in sync
		// with the gain log during the off period, so a later `gain` flush (where
		// ReportImpact rebuilds Programs from the full gain summary) cannot
		// re-include off-period detail after a re-enable. Aggregate totals and agent
		// buckets still accumulate as website stats.
		st.LastReported.Programs = mergeBuckets(st.LastReported.Programs, rec.Programs)
		rec.Programs = nil
	}
	addTotals(&st.Pending, rec)
	if !shouldFlushPending(st, now) {
		if st.LastAttempt == "" {
			st.LastAttempt = now.Format(time.RFC3339)
		}
		if err := writeState(st); err != nil {
			return Result{}, err
		}
		return Result{Noop: true}, nil
	}

	sanitizeTotals(&st.Pending) // belt-and-suspenders: nothing unsafe reaches the wire
	payload := buildImpactPayload(st.Pending, cfg)
	st.LastAttempt = now.Format(time.RFC3339)
	if err := writeState(st); err != nil {
		return Result{}, err
	}
	if err := sendPayload(payload); err != nil {
		return Result{}, err
	}
	addTotals(&st.LastReported, st.Pending)
	st.Pending = Totals{}
	if err := writeState(st); err != nil {
		return Result{}, err
	}
	return Result{Sent: true}, nil
}

func totalsFromSummary(summary *gain.Summary) Totals {
	t := Totals{
		Commands:     int64(summary.Commands),
		RawBytes:     summary.RawBytes,
		EmittedBytes: summary.EmittedBytes,
		BytesSaved:   summary.SavedBytes,
		TokensSaved:  approxTokens(summary.SavedBytes),
		Programs:     map[string]ProgramTotals{},
	}
	for _, p := range summary.ByProgram {
		name := safeProgramName(p.Program)
		if name == "" {
			continue
		}
		t.Programs[name] = ProgramTotals{
			Count:        int64(p.Count),
			RawBytes:     p.RawBytes,
			EmittedBytes: p.EmittedBytes,
			BytesSaved:   p.SavedBytes,
			TokensSaved:  approxTokens(p.SavedBytes),
		}
	}
	for _, a := range summary.ByAgent {
		name := safeAgentName(a.Agent)
		if name == "" {
			continue // unattributed: counted in scalar totals, no agent bucket
		}
		if t.Agents == nil {
			t.Agents = map[string]ProgramTotals{}
		}
		t.Agents[name] = ProgramTotals{
			Count:        int64(a.Commands),
			RawBytes:     a.RawBytes,
			EmittedBytes: a.EmittedBytes,
			BytesSaved:   a.SavedBytes,
			TokensSaved:  approxTokens(a.SavedBytes),
		}
	}
	return t
}

func totalsFromCommand(command, agentName string, rawBytes, emittedBytes int) Totals {
	// A synthetic on_empty message can make emitted exceed raw; never record
	// negative savings.
	saved := rawBytes - emittedBytes
	if saved < 0 {
		saved = 0
	}
	bucket := ProgramTotals{
		Count:        1,
		RawBytes:     int64(rawBytes),
		EmittedBytes: int64(emittedBytes),
		BytesSaved:   int64(saved),
		TokensSaved:  approxTokens(int64(saved)),
	}
	t := Totals{
		Commands:     1,
		RawBytes:     int64(rawBytes),
		EmittedBytes: int64(emittedBytes),
		BytesSaved:   int64(saved),
		TokensSaved:  approxTokens(int64(saved)),
		Programs:     map[string]ProgramTotals{},
	}
	if program := safeProgramName(gain.ProgramName(command)); program != "" {
		t.Programs[program] = bucket
	}
	// Attribute to the invoking agent when known. Unattributed commands add to the
	// scalar totals but no agent bucket, so the site can show coverage honestly.
	if ag := safeAgentName(agentName); ag != "" {
		t.Agents = map[string]ProgramTotals{ag: bucket}
	}
	return t
}

func addTotals(dst *Totals, src Totals) {
	dst.Commands += src.Commands
	dst.RawBytes += src.RawBytes
	dst.EmittedBytes += src.EmittedBytes
	dst.BytesSaved += src.BytesSaved
	dst.TokensSaved += src.TokensSaved
	dst.Programs = mergeBuckets(dst.Programs, src.Programs)
	dst.Agents = mergeBuckets(dst.Agents, src.Agents)
}

// mergeBuckets folds src's per-key counters into dst, returning the merged map
// (nil when empty). Shared by the per-program and per-agent breakdowns.
func mergeBuckets(dst, src map[string]ProgramTotals) map[string]ProgramTotals {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]ProgramTotals{}
	}
	for name, p := range src {
		cur := dst[name]
		cur.Count += p.Count
		cur.RawBytes += p.RawBytes
		cur.EmittedBytes += p.EmittedBytes
		cur.BytesSaved += p.BytesSaved
		cur.TokensSaved += p.TokensSaved
		dst[name] = cur
	}
	if len(dst) == 0 {
		return nil
	}
	return dst
}

func shouldFlushPending(st stateFile, now time.Time) bool {
	if totalsEmpty(st.Pending) {
		return false
	}
	last := parseStateTime(st.LastAttempt)
	// First flush (no prior attempt): require a meaningful batch so a single
	// small command does not immediately hit the wire.
	if last.IsZero() {
		return st.Pending.Commands >= autoFlushCommandThreshold ||
			st.Pending.BytesSaved >= autoFlushSavedThreshold
	}
	// After any prior attempt the interval is the sole gate: it both rate-limits
	// the endpoint and throttles retries after a failed send. The volume
	// thresholds only ever bring the FIRST flush forward, never a later one.
	return now.Sub(last) >= autoFlushInterval
}

func parseStateTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func subtractTotals(current, previous Totals) Totals {
	if current.Commands < previous.Commands ||
		current.RawBytes < previous.RawBytes ||
		current.EmittedBytes < previous.EmittedBytes ||
		current.BytesSaved < previous.BytesSaved ||
		current.TokensSaved < previous.TokensSaved {
		return current
	}
	delta := Totals{
		Commands:     current.Commands - previous.Commands,
		RawBytes:     current.RawBytes - previous.RawBytes,
		EmittedBytes: current.EmittedBytes - previous.EmittedBytes,
		BytesSaved:   current.BytesSaved - previous.BytesSaved,
		TokensSaved:  current.TokensSaved - previous.TokensSaved,
		Programs:     subtractBuckets(current.Programs, previous.Programs),
		Agents:       subtractBuckets(current.Agents, previous.Agents),
	}
	return delta
}

func subtractBuckets(current, previous map[string]ProgramTotals) map[string]ProgramTotals {
	if len(current) == 0 {
		return nil
	}
	out := map[string]ProgramTotals{}
	for name, cur := range current {
		prev := previous[name]
		if cur.Count < prev.Count ||
			cur.RawBytes < prev.RawBytes ||
			cur.EmittedBytes < prev.EmittedBytes ||
			cur.BytesSaved < prev.BytesSaved ||
			cur.TokensSaved < prev.TokensSaved {
			out[name] = cur
			continue
		}
		pd := ProgramTotals{
			Count:        cur.Count - prev.Count,
			RawBytes:     cur.RawBytes - prev.RawBytes,
			EmittedBytes: cur.EmittedBytes - prev.EmittedBytes,
			BytesSaved:   cur.BytesSaved - prev.BytesSaved,
			TokensSaved:  cur.TokensSaved - prev.TokensSaved,
		}
		if !programEmpty(pd) {
			out[name] = pd
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func totalsEmpty(t Totals) bool {
	return t.Commands == 0 &&
		t.RawBytes == 0 &&
		t.EmittedBytes == 0 &&
		t.BytesSaved == 0 &&
		t.TokensSaved == 0 &&
		len(t.Programs) == 0 &&
		len(t.Agents) == 0
}

func programEmpty(p ProgramTotals) bool {
	return p.Count == 0 && p.RawBytes == 0 && p.EmittedBytes == 0 && p.BytesSaved == 0 && p.TokensSaved == 0
}

func enabled(cfg Config) (bool, bool) {
	if v := strings.TrimSpace(strings.ToLower(os.Getenv(envEnabled))); v != "" {
		switch v {
		case "0", "false", "off", "no":
			return false, true
		case "1", "true", "on", "yes":
			return true, true
		}
	}
	if cfg.Enabled != nil {
		return *cfg.Enabled, false
	}
	// Opt-out: anonymous aggregate telemetry is ON by default and stays on until
	// the user explicitly disables it. A nil choice means "never decided", treated
	// as on. An explicit {enabled:false} (the off-switch) is honored here and is
	// never reversed by an update: only nil is migrated to on, never false.
	return true, false
}

func endpoint() string {
	if v := strings.TrimSpace(os.Getenv(envURL)); v != "" {
		return v
	}
	return DefaultEndpoint
}

func readConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		// Default config.
	default:
		return Config{}, err
	}
	return cfg, nil
}

func writeConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	return writeJSON(path, cfg)
}

func readState() (stateFile, error) {
	path, err := statePath()
	if err != nil {
		return stateFile{}, err
	}
	var st stateFile
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &st); err != nil {
				return stateFile{}, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		// Empty state.
	default:
		return stateFile{}, err
	}
	// Fold any pre-upgrade private program/agent keys onto the allowlist so a
	// telemetry-state.json written before this fix cannot leak them on the next
	// send. New inserts are already allowlisted at the totals sites; this covers
	// the at-rest state.
	sanitizeTotals(&st.LastReported)
	sanitizeTotals(&st.Pending)
	if st.LastReported.Programs == nil {
		st.LastReported.Programs = map[string]ProgramTotals{}
	}
	if st.Pending.Programs == nil {
		st.Pending.Programs = map[string]ProgramTotals{}
	}
	return st, nil
}

func writeState(st stateFile) error {
	path, err := statePath()
	if err != nil {
		return err
	}
	return writeJSON(path, st)
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ctx-wire-telemetry-*")
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
	return os.Rename(tmpName, path)
}

func configPath() (string, error) {
	if v := os.Getenv(envConfig); v != "" {
		return v, nil
	}
	base, err := paths.ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", "telemetry.json"), nil
}

func statePath() (string, error) {
	if v := os.Getenv(envState); v != "" {
		return v, nil
	}
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", "telemetry-state.json"), nil
}

func postJSON(payload any) error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint(), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ctx-wire")
	client := &http.Client{Timeout: requestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telemetry endpoint returned %s", resp.Status)
	}
	return nil
}

func normalizeProgram(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > 64 {
		return ""
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '+' || r == '-' {
			continue
		}
		return ""
	}
	return name
}

func approxTokens(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

// ProgramNames returns sorted program names in a Totals value. It is used by
// tests and keeps map-order assumptions out of assertions.
func ProgramNames(t Totals) []string {
	names := make([]string, 0, len(t.Programs))
	for name := range t.Programs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
