package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"ctx-wire/internal/gain"
)

func TestReportImpactSendsDeltaOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	// Steady state: install already reported, so impact flushes don't also backfill.
	if err := writeConfig(Config{InstallReported: true}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	var payloads []map[string]any
	restoreSender(t, func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		payloads = append(payloads, payload)
		return nil
	})

	first := summary(10, 1000, 600, 400, []gain.CommandStat{
		{Program: "cat", Count: 7, RawBytes: 700, EmittedBytes: 400, SavedBytes: 300},
		{Program: "rg", Count: 3, RawBytes: 300, EmittedBytes: 200, SavedBytes: 100},
	})
	res, err := ReportImpact(first)
	if err != nil {
		t.Fatalf("ReportImpact first: %v", err)
	}
	if !res.Sent {
		t.Fatal("expected first report to send")
	}

	second := summary(15, 1600, 900, 700, []gain.CommandStat{
		{Program: "cat", Count: 9, RawBytes: 900, EmittedBytes: 450, SavedBytes: 450},
		{Program: "rg", Count: 6, RawBytes: 600, EmittedBytes: 450, SavedBytes: 150},
		{Program: "git", Count: 1, RawBytes: 100, EmittedBytes: 0, SavedBytes: 100},
	})
	res, err = ReportImpact(second)
	if err != nil {
		t.Fatalf("ReportImpact second: %v", err)
	}
	if !res.Sent {
		t.Fatal("expected second report to send")
	}

	if len(payloads) != 2 {
		t.Fatalf("payloads = %d, want 2", len(payloads))
	}
	if got := int64(payloads[0]["commands"].(float64)); got != 10 {
		t.Fatalf("first commands = %d, want 10", got)
	}
	if got := int64(payloads[1]["commands"].(float64)); got != 5 {
		t.Fatalf("second commands delta = %d, want 5", got)
	}
	if got := int64(payloads[1]["raw_bytes"].(float64)); got != 600 {
		t.Fatalf("second raw delta = %d, want 600", got)
	}
	programs := payloads[1]["programs"].(map[string]any)
	cat := programs["cat"].(map[string]any)
	if got := int64(cat["count"].(float64)); got != 2 {
		t.Fatalf("cat count delta = %d, want 2", got)
	}
	git := programs["git"].(map[string]any)
	if got := int64(git["bytes_saved"].(float64)); got != 100 {
		t.Fatalf("git saved delta = %d, want 100", got)
	}

	res, err = ReportImpact(second)
	if err != nil {
		t.Fatalf("ReportImpact third: %v", err)
	}
	if !res.Noop {
		t.Fatal("expected unchanged summary to be noop")
	}
	if len(payloads) != 2 {
		t.Fatalf("unexpected duplicate payloads = %d", len(payloads))
	}
}

func TestReportInstallMarksOnlyAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	var count int
	restoreSender(t, func(payload any) error {
		count++
		return nil
	})

	res, err := ReportInstall("")
	if err != nil {
		t.Fatalf("ReportInstall first: %v", err)
	}
	if !res.Sent || !res.InstallReported || !res.MachineFirst {
		t.Fatalf("first result = %#v", res)
	}
	res, err = ReportInstall("")
	if err != nil {
		t.Fatalf("ReportInstall second: %v", err)
	}
	if !res.Noop || !res.InstallReported {
		t.Fatalf("second result = %#v", res)
	}
	if count != 1 {
		t.Fatalf("install reports = %d, want 1", count)
	}
}

// TestReportInstallEnabledByDefault pins the aggregate default: with no explicit
// choice telemetry is on, so an init reports the install and latches it.
func TestReportInstallEnabledByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	var count int
	restoreSender(t, func(payload any) error {
		count++
		return nil
	})

	res, err := ReportInstall("claude")
	if err != nil {
		t.Fatalf("ReportInstall: %v", err)
	}
	if res.Disabled {
		t.Fatalf("ReportInstall under aggregate default = %#v, want sent (not Disabled)", res)
	}
	if count != 1 {
		t.Fatalf("install reports sent by default = %d, want 1", count)
	}
	status, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.InstallReported {
		t.Fatal("a sent install report must mark InstallReported")
	}
}

// TestReportInstallStillSendsWithLegacyDisabledConfig pins the current product
// model: old configs may contain enabled=false, but aggregate install telemetry
// still flows. The only public off-ramp is the command-breakdown toggle.
func TestReportInstallStillSendsWithLegacyDisabledConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	legacyOff := false
	if err := writeConfig(Config{Enabled: &legacyOff}); err != nil {
		t.Fatalf("seed legacy disabled config: %v", err)
	}
	var count int
	restoreSender(t, func(payload any) error {
		count++
		return nil
	})

	res, err := ReportInstall("claude")
	if err != nil {
		t.Fatalf("ReportInstall: %v", err)
	}
	if !res.Sent || res.Disabled {
		t.Fatalf("ReportInstall with legacy disabled config = %#v, want sent", res)
	}
	if count != 1 {
		t.Fatalf("install reports sent = %d, want 1", count)
	}
	status, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Enabled || !status.InstallReported {
		t.Fatalf("status = %#v, want enabled and install reported", status)
	}
}

// TestReportInstallPerAgent verifies every successful agent init reports an
// install event, including repeats for the same agent. The local config only
// controls the one-time telemetry notice and remembers which agents were seen.
func TestReportInstallPerAgent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	var sent []installPayload
	restoreSender(t, func(payload any) error {
		if p, ok := payload.(installPayload); ok {
			sent = append(sent, p)
		}
		return nil
	})

	// First init wires claude: one event carrying both the install count and the agent.
	res, err := ReportInstall("claude")
	if err != nil {
		t.Fatalf("claude: %v", err)
	}
	if !res.Sent || !res.MachineFirst || !res.AgentReported {
		t.Fatalf("claude first = %#v", res)
	}
	// Second init wires codex: no first-install notice, but it still counts.
	res, err = ReportInstall("codex")
	if err != nil {
		t.Fatalf("codex: %v", err)
	}
	if !res.Sent || res.MachineFirst || !res.AgentReported {
		t.Fatalf("codex = %#v", res)
	}
	// Re-running init for an already-reported agent still counts another install.
	res, err = ReportInstall("claude")
	if err != nil {
		t.Fatalf("claude repeat: %v", err)
	}
	if !res.Sent || res.Noop || res.MachineFirst || !res.AgentReported {
		t.Fatalf("claude repeat = %#v", res)
	}
	// A binary-only init is a no-op too once the machine is latched.
	if res, _ := ReportInstall(""); !res.Noop {
		t.Fatalf("self after latch = %#v", res)
	}

	if len(sent) != 3 {
		t.Fatalf("sent %d events, want 3", len(sent))
	}
	if sent[0].Agent != "claude" || !sent[0].Machine {
		t.Errorf("event 0 = %#v, want claude counted", sent[0])
	}
	if sent[1].Agent != "codex" || !sent[1].Machine {
		t.Errorf("event 1 = %#v, want codex counted", sent[1])
	}
	if sent[2].Agent != "claude" || !sent[2].Machine {
		t.Errorf("event 2 = %#v, want repeated claude counted", sent[2])
	}
}

func TestSetEnabledFalseDisablesOnlyCommandBreakdown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	var sent []impactPayload
	restoreSender(t, func(v any) error {
		if p, ok := v.(impactPayload); ok {
			sent = append(sent, p)
		}
		return nil
	})

	if err := SetEnabled(false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	status, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Enabled {
		t.Fatal("aggregate telemetry must stay enabled")
	}
	if status.ShareImprovements {
		t.Fatal("SetEnabled(false) should disable only the command breakdown")
	}
	res, err := ReportImpact(summary(1, 100, 50, 50, []gain.CommandStat{
		{Program: "rg", Count: 1, RawBytes: 100, EmittedBytes: 50, SavedBytes: 50},
	}))
	if err != nil {
		t.Fatalf("ReportImpact: %v", err)
	}
	if !res.Sent || len(sent) != 1 {
		t.Fatalf("result=%#v sent=%d, want sent aggregate payload", res, len(sent))
	}
	if sent[0].Programs != nil {
		t.Fatalf("command breakdown should be withheld, got %v", sent[0].Programs)
	}
}

func TestRecordCommandFlushesAfterInterval(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	base := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	restoreClock(t, base)
	var payloads []map[string]any
	restoreSender(t, func(v any) error {
		payloads = append(payloads, payloadMap(t, v))
		return nil
	})

	res, err := RecordCommand("cat README.md", "claude", 1000, 200)
	if err != nil {
		t.Fatalf("RecordCommand first: %v", err)
	}
	if !res.Noop || len(payloads) != 0 {
		t.Fatalf("first result = %#v payloads=%d, want pending noop", res, len(payloads))
	}

	restoreClock(t, base.Add(autoFlushInterval-time.Minute))
	res, err = RecordCommand("rg TODO .", "claude", 2000, 1000)
	if err != nil {
		t.Fatalf("RecordCommand second: %v", err)
	}
	if !res.Noop || len(payloads) != 0 {
		t.Fatalf("second result = %#v payloads=%d, want pending noop", res, len(payloads))
	}

	restoreClock(t, base.Add(autoFlushInterval+time.Second))
	res, err = RecordCommand("git status", "claude", 100, 50)
	if err != nil {
		t.Fatalf("RecordCommand third: %v", err)
	}
	if !res.Sent || len(payloads) != 1 {
		t.Fatalf("third result = %#v payloads=%d, want one send", res, len(payloads))
	}
	if got := int64(payloads[0]["commands"].(float64)); got != 3 {
		t.Fatalf("commands payload = %d, want 3", got)
	}
	programs := payloads[0]["programs"].(map[string]any)
	if got := int64(programs["cat"].(map[string]any)["count"].(float64)); got != 1 {
		t.Fatalf("cat count = %d, want 1", got)
	}
	if got := int64(programs["rg"].(map[string]any)["bytes_saved"].(float64)); got != 1000 {
		t.Fatalf("rg saved = %d, want 1000", got)
	}
	st, err := readState()
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if !totalsEmpty(st.Pending) {
		t.Fatalf("pending after send = %+v, want empty", st.Pending)
	}
	if st.LastReported.Commands != 3 {
		t.Fatalf("last reported commands = %d, want 3", st.LastReported.Commands)
	}
}

func TestRecordCommandFlushesLargeSavedBytesImmediately(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	restoreClock(t, time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC))
	var payloads []map[string]any
	restoreSender(t, func(v any) error {
		payloads = append(payloads, payloadMap(t, v))
		return nil
	})

	res, err := RecordCommand("grep huge file", "claude", 11<<20, 0)
	if err != nil {
		t.Fatalf("RecordCommand: %v", err)
	}
	if !res.Sent || len(payloads) != 1 {
		t.Fatalf("result = %#v payloads=%d, want immediate send", res, len(payloads))
	}
	if got := int64(payloads[0]["bytes_saved"].(float64)); got != 11<<20 {
		t.Fatalf("bytes_saved = %d, want %d", got, int64(11<<20))
	}
}

func TestRecordCommandFailureKeepsPendingAndThrottlesRetry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	base := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	restoreClock(t, base)
	var attempts int
	restoreSender(t, func(v any) error {
		attempts++
		return errors.New("offline")
	})

	if _, err := RecordCommand("cat one", "claude", 1000, 0); err != nil {
		t.Fatalf("RecordCommand first: %v", err)
	}
	restoreClock(t, base.Add(autoFlushInterval+time.Minute))
	if _, err := RecordCommand("cat two", "claude", 1000, 0); err == nil {
		t.Fatal("second RecordCommand should return send error")
	}
	if attempts != 1 {
		t.Fatalf("attempts after failed flush = %d, want 1", attempts)
	}
	st, err := readState()
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if st.Pending.Commands != 2 {
		t.Fatalf("pending commands after failed flush = %d, want 2", st.Pending.Commands)
	}

	restoreClock(t, base.Add(autoFlushInterval+time.Minute+time.Second))
	res, err := RecordCommand("cat three", "claude", 1000, 0)
	if err != nil {
		t.Fatalf("third RecordCommand should be throttled, got error: %v", err)
	}
	if !res.Noop || attempts != 1 {
		t.Fatalf("third result=%#v attempts=%d, want throttled noop", res, attempts)
	}
	st, err = readState()
	if err != nil {
		t.Fatalf("readState final: %v", err)
	}
	if st.Pending.Commands != 3 {
		t.Fatalf("pending commands final = %d, want 3", st.Pending.Commands)
	}
}

// TestBuildImpactPayloadGatesPrograms pins the single gating point both send
// paths use: command breakdown off withholds Programs but keeps the website
// stats; on (or the default nil) includes Programs.
func TestBuildImpactPayloadGatesPrograms(t *testing.T) {
	delta := Totals{
		Commands: 10, RawBytes: 1000, EmittedBytes: 200, BytesSaved: 800, TokensSaved: 200,
		Programs: map[string]ProgramTotals{"rg": {Count: 10, BytesSaved: 800}},
		Agents:   map[string]ProgramTotals{"claude": {Count: 10, BytesSaved: 800}},
	}
	off := false
	pOff := buildImpactPayload(delta, Config{ShareImprovements: &off})
	if pOff.Programs != nil {
		t.Errorf("command breakdown off: Programs must be withheld, got %v", pOff.Programs)
	}
	if pOff.Agents == nil || pOff.Commands == 0 {
		t.Error("command breakdown off: website stats (agents, totals) must still flow")
	}
	on := true
	if buildImpactPayload(delta, Config{ShareImprovements: &on}).Programs == nil {
		t.Error("improvements on: Programs must be present")
	}
	if buildImpactPayload(delta, Config{}).Programs == nil {
		t.Error("improvements default (nil): Programs must be present")
	}
	// Legacy enabled=false withholds Programs but keeps aggregate flowing.
	legacyOff := false
	pLegacy := buildImpactPayload(delta, Config{Enabled: &legacyOff})
	if pLegacy.Programs != nil {
		t.Errorf("legacy enabled=false: Programs must be withheld, got %v", pLegacy.Programs)
	}
	if pLegacy.Agents == nil || pLegacy.Commands == 0 {
		t.Error("legacy enabled=false: aggregate (agents, totals) must still flow")
	}
	// Explicit ShareImprovements overrides the legacy flag.
	if buildImpactPayload(delta, Config{Enabled: &legacyOff, ShareImprovements: &on}).Programs == nil {
		t.Error("explicit ShareImprovements=true must override legacy enabled=false")
	}
}

// TestRecordCommandAutoFlushRespectsImprovementsOff is the integration guard for
// the bug this almost shipped with: the hook auto-flush is a SEPARATE send path
// from the manual gain flush, so it must ALSO withhold the per-command breakdown
// when the command breakdown is off.
func TestRecordCommandAutoFlushRespectsImprovementsOff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	off := false
	if err := writeConfig(Config{ShareImprovements: &off}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	var sent []impactPayload
	restoreSender(t, func(v any) error {
		if p, ok := v.(impactPayload); ok {
			sent = append(sent, p)
		}
		return nil
	})
	// One big command crosses the saved-bytes auto-flush threshold.
	res, err := RecordCommand("rg pattern .", "claude", 11<<20, 0)
	if err != nil {
		t.Fatalf("RecordCommand: %v", err)
	}
	if !res.Sent {
		t.Fatalf("expected auto-flush, got %#v", res)
	}
	if len(sent) != 1 {
		t.Fatalf("impact payloads = %d, want 1", len(sent))
	}
	if sent[0].Programs != nil {
		t.Errorf("auto-flush with command breakdown off must withhold Programs, got %v", sent[0].Programs)
	}
	if sent[0].Agents == nil {
		t.Error("auto-flush must still send Agents (website stat)")
	}
}

// TestImprovementsOffPendingNotLeakedOnReenable is the state-dependent edge: per-
// command detail accumulated while improvements are off must NOT ship when the
// user turns them back on before the next flush. Off-period commands still count
// toward the aggregate (a website stat), but their per-command bucket must not.
func TestImprovementsOffPendingNotLeakedOnReenable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	if err := writeConfig(Config{InstallReported: true}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	base := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	restoreClock(t, base)
	var sent []impactPayload
	restoreSender(t, func(v any) error {
		if p, ok := v.(impactPayload); ok {
			sent = append(sent, p)
		}
		return nil
	})

	if err := SetShareImprovements(false); err != nil {
		t.Fatalf("SetShareImprovements(false): %v", err)
	}
	// Off period: small commands under the volume threshold, so they accumulate.
	for i := 0; i < 3; i++ {
		if _, err := RecordCommand("rg off-period .", "claude", 500, 100); err != nil {
			t.Fatalf("RecordCommand off: %v", err)
		}
	}
	if len(sent) != 0 {
		t.Fatalf("no flush expected during off accumulation, got %d", len(sent))
	}

	// Re-enable, advance past the flush interval, then run one on-period command.
	if err := SetShareImprovements(true); err != nil {
		t.Fatalf("SetShareImprovements(true): %v", err)
	}
	restoreClock(t, base.Add(autoFlushInterval+time.Minute))
	if _, err := RecordCommand("rg on-period .", "claude", 800, 100); err != nil {
		t.Fatalf("RecordCommand flush: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("expected one flush after re-enable, got %d", len(sent))
	}
	rg, ok := sent[0].Programs["rg"]
	if !ok {
		t.Fatalf("on-period flush should carry the rg bucket, got %v", sent[0].Programs)
	}
	if rg.Count != 1 {
		t.Errorf("rg bucket count = %d, want 1 (off-period detail must not leak)", rg.Count)
	}
	if sent[0].Commands != 4 {
		t.Errorf("aggregate Commands = %d, want 4 (off-period still counts as a stat)", sent[0].Commands)
	}
}

// TestMigrationNoticeIfPending pins the one-time non-interactive notice AND the
// separate-marker guarantee: firing it must NOT suppress the interactive notice,
// so a swallowed line never means the human sees nothing.
func TestMigrationNoticeIfPending(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))

	if got := MigrationNoticeIfPending(); got == "" {
		t.Fatal("undecided user should get the one-time migration notice")
	}
	if got := MigrationNoticeIfPending(); got != "" {
		t.Errorf("migration notice must not repeat, got %q", got)
	}
	if !ShouldPreviewConsent() {
		t.Error("interactive notice must still fire after the migration notice (separate markers)")
	}
}

// TestMigrationNoticeStillShowsAfterCommandBreakdownChoice verifies the
// disclosure is about aggregate telemetry, not just the optional command
// breakdown. Toggling that breakdown must not suppress the one-time notice.
func TestMigrationNoticeStillShowsAfterCommandBreakdownChoice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	if err := SetShareImprovements(false); err != nil {
		t.Fatalf("SetShareImprovements: %v", err)
	}
	if got := MigrationNoticeIfPending(); got == "" {
		t.Fatal("command-breakdown choice must not suppress aggregate telemetry notice")
	}
}

// TestImprovementsOffGainBackfillNotLeaked is the gain-log path of the same class:
// ReportImpact (the `ctx-wire gain` flush) rebuilds Programs from the FULL gain
// summary, so off-period detail can backfill via the delta after a re-enable even
// though the hook path is gated. Off -> hook commands -> on -> gain must not ship
// the off-period buckets.
func TestImprovementsOffGainBackfillNotLeaked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	if err := writeConfig(Config{InstallReported: true}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	var sent []impactPayload
	restoreSender(t, func(v any) error {
		if p, ok := v.(impactPayload); ok {
			sent = append(sent, p)
		}
		return nil
	})

	if err := SetShareImprovements(false); err != nil {
		t.Fatalf("SetShareImprovements(false): %v", err)
	}
	// Three off-period commands via the hook (the gain log would record them too).
	for i := 0; i < 3; i++ {
		if _, err := RecordCommand("rg off .", "claude", 500, 100); err != nil {
			t.Fatalf("RecordCommand off: %v", err)
		}
	}

	// Re-enable, then a gain flush whose summary includes the 3 off-period commands
	// plus 1 on-period command (the full gain log).
	if err := SetShareImprovements(true); err != nil {
		t.Fatalf("SetShareImprovements(true): %v", err)
	}
	res, err := ReportImpact(summary(4, 1600, 400, 1200, []gain.CommandStat{
		{Program: "rg", Count: 4, RawBytes: 1600, EmittedBytes: 400, SavedBytes: 1200},
	}))
	if err != nil {
		t.Fatalf("ReportImpact: %v", err)
	}
	if !res.Sent {
		t.Fatalf("expected a send, got %#v", res)
	}
	if len(sent) != 1 {
		t.Fatalf("payloads = %d, want 1", len(sent))
	}
	rg, ok := sent[0].Programs["rg"]
	if !ok {
		t.Fatalf("expected rg bucket, got %v", sent[0].Programs)
	}
	if rg.Count != 1 {
		t.Errorf("rg bucket count = %d, want 1 (3 off-period commands must not backfill via gain)", rg.Count)
	}
	if sent[0].Commands != 4 {
		t.Errorf("aggregate Commands = %d, want 4 (off-period still counts as a stat)", sent[0].Commands)
	}
}

// TestReportImpactBackfillsInstall verifies the init-then-enable gap is closed:
// when telemetry is on but no install was reported yet, the first impact flush
// also reports the install once; later flushes do not repeat it.
func TestReportImpactBackfillsInstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	var installs, impacts int
	restoreSender(t, func(v any) error {
		switch v.(type) {
		case installPayload:
			installs++
		case impactPayload:
			impacts++
		}
		return nil
	})

	if _, err := ReportImpact(summary(5, 1000, 200, 800, []gain.CommandStat{
		{Program: "rg", Count: 5, RawBytes: 1000, EmittedBytes: 200, SavedBytes: 800},
	})); err != nil {
		t.Fatalf("ReportImpact first: %v", err)
	}
	if installs != 1 || impacts != 1 {
		t.Fatalf("first flush: installs=%d impacts=%d, want 1 and 1 (install backfilled)", installs, impacts)
	}
	if status, err := GetStatus(); err != nil || !status.InstallReported {
		t.Fatalf("install must latch after backfill (err=%v)", err)
	}
	if _, err := ReportImpact(summary(8, 1600, 400, 1200, []gain.CommandStat{
		{Program: "rg", Count: 8, RawBytes: 1600, EmittedBytes: 400, SavedBytes: 1200},
	})); err != nil {
		t.Fatalf("ReportImpact second: %v", err)
	}
	if installs != 1 {
		t.Fatalf("install backfilled %d times total, want exactly 1", installs)
	}
	if impacts != 2 {
		t.Fatalf("impacts=%d after two flushes, want 2", impacts)
	}
}

func TestReportImpactClearsPendingAfterManualFlush(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	if err := writeConfig(Config{InstallReported: true}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	restoreClock(t, time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC))
	var payloads []map[string]any
	restoreSender(t, func(v any) error {
		payloads = append(payloads, payloadMap(t, v))
		return nil
	})

	if _, err := RecordCommand("cat README.md", "claude", 1000, 200); err != nil {
		t.Fatalf("RecordCommand: %v", err)
	}
	res, err := ReportImpact(summary(1, 1000, 200, 800, []gain.CommandStat{
		{Program: "cat", Count: 1, RawBytes: 1000, EmittedBytes: 200, SavedBytes: 800},
	}))
	if err != nil {
		t.Fatalf("ReportImpact: %v", err)
	}
	if !res.Sent || len(payloads) != 1 {
		t.Fatalf("ReportImpact result=%#v payloads=%d, want one send", res, len(payloads))
	}
	st, err := readState()
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if !totalsEmpty(st.Pending) {
		t.Fatalf("pending after ReportImpact = %+v, want empty", st.Pending)
	}
	if st.LastReported.Commands != 1 {
		t.Fatalf("last reported commands = %d, want 1", st.LastReported.Commands)
	}
}

// TestRecordCommandPerAgentBreakdown verifies the flushed payload carries a
// per-agent token breakdown and that unattributed commands count toward the
// totals without inventing an agent bucket.
func TestRecordCommandPerAgentBreakdown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	base := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	restoreClock(t, base)
	var payloads []map[string]any
	restoreSender(t, func(v any) error {
		payloads = append(payloads, payloadMap(t, v))
		return nil
	})

	if _, err := RecordCommand("cat README.md", "claude", 1000, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordCommand("rg TODO .", "codex", 2000, 1000); err != nil {
		t.Fatal(err)
	}
	// Unattributed: counts in the scalar totals, no agent bucket.
	restoreClock(t, base.Add(autoFlushInterval+time.Second))
	res, err := RecordCommand("git status", "", 100, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Sent || len(payloads) != 1 {
		t.Fatalf("result = %#v payloads=%d, want one send", res, len(payloads))
	}
	if got := int64(payloads[0]["commands"].(float64)); got != 3 {
		t.Fatalf("commands = %d, want 3 (including the unattributed one)", got)
	}
	agents, ok := payloads[0]["agents"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing agents breakdown: %v", payloads[0])
	}
	if len(agents) != 2 {
		t.Fatalf("agents = %v, want exactly claude and codex (no unattributed bucket)", agents)
	}
	if got := int64(agents["claude"].(map[string]any)["bytes_saved"].(float64)); got != 800 {
		t.Errorf("claude bytes_saved = %d, want 800", got)
	}
	if got := int64(agents["codex"].(map[string]any)["count"].(float64)); got != 1 {
		t.Errorf("codex count = %d, want 1", got)
	}
	if _, exists := agents[""]; exists {
		t.Error("unattributed command must not create an empty-key agent bucket")
	}
}

func summary(commands int, raw, emitted, saved int64, programs []gain.CommandStat) *gain.Summary {
	return &gain.Summary{
		Commands:     commands,
		RawBytes:     raw,
		EmittedBytes: emitted,
		SavedBytes:   saved,
		ByProgram:    programs,
	}
}

func restoreSender(t *testing.T, fn func(any) error) {
	t.Helper()
	prev := sendPayload
	sendPayload = fn
	t.Cleanup(func() { sendPayload = prev })
}

func restoreClock(t *testing.T, value time.Time) {
	t.Helper()
	prev := clockNow
	clockNow = func() time.Time { return value }
	t.Cleanup(func() { clockNow = prev })
}

func payloadMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

// TestBuildImpactPayloadCapsWireBuckets pins the fix for the oversized-payload
// data-loss bug: a delta with many distinct programs must not build a payload
// the worker rejects wholesale. The breakdown is capped to <= 50 buckets by
// folding the tail into "other", and no savings are lost to the fold.
func TestBuildImpactPayloadCapsWireBuckets(t *testing.T) {
	on := true
	cfg := Config{ShareImprovements: &on}
	delta := Totals{Commands: 200, Programs: map[string]ProgramTotals{}}
	var wantSaved int64
	for i := 0; i < 130; i++ {
		s := int64(10_000 - i) // strictly decreasing so the top-N is deterministic
		delta.Programs[fmt.Sprintf("prog%03d", i)] = ProgramTotals{Count: 1, RawBytes: s, BytesSaved: s, TokensSaved: s}
		wantSaved += s
	}
	p := buildImpactPayload(delta, cfg)
	// Literal 50, not maxWireBuckets: removing the cap must fail this
	// behaviorally (uncapped len would be 130), not just fail to compile.
	if len(p.Programs) > 50 {
		t.Fatalf("wire programs = %d, want <= 50 (uncapped would be 130, which the worker rejects)", len(p.Programs))
	}
	if _, ok := p.Programs["prog000"]; !ok {
		t.Errorf("highest-savings program prog000 must stay itemized")
	}
	if _, ok := p.Programs[otherBucket]; !ok {
		t.Errorf("folded tail must appear as %q", otherBucket)
	}
	var gotSaved int64
	for _, b := range p.Programs {
		gotSaved += b.BytesSaved
	}
	if gotSaved != wantSaved {
		t.Errorf("folded saved = %d, want %d: the cap must not lose any savings", gotSaved, wantSaved)
	}
}

// TestCapBucketsSmallMapUntouched confirms the common case (breakdown already
// within the wire cap) is returned unchanged.
func TestCapBucketsSmallMapUntouched(t *testing.T) {
	m := map[string]ProgramTotals{
		"git": {Count: 3, BytesSaved: 30},
		"rg":  {Count: 2, BytesSaved: 20},
	}
	got := capBuckets(m)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (small map must pass through)", len(got))
	}
	if _, ok := got[otherBucket]; ok {
		t.Errorf("small map must not synthesize an %q bucket", otherBucket)
	}
}
