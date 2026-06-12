package telemetry

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"ctx-wire/internal/gain"
)

func TestReportImpactSendsDeltaOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envEnabled, "1")
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
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
	t.Setenv(envEnabled, "1")
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

func TestReportInstallDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envEnabled, "")
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
	if !res.Disabled {
		t.Fatalf("ReportInstall default = %#v, want Disabled", res)
	}
	if count != 0 {
		t.Fatalf("install reports sent by default = %d, want 0", count)
	}
	status, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.InstallReported {
		t.Fatal("disabled install report must not mark InstallReported")
	}
}

// TestReportInstallPerAgent verifies every successful agent init reports an
// install event, including repeats for the same agent. The local config only
// controls the one-time telemetry notice and remembers which agents were seen.
func TestReportInstallPerAgent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envEnabled, "1")
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

func TestTelemetryCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	t.Setenv(envURL, "http://127.0.0.1:1")

	if err := SetEnabled(false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	status, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Enabled {
		t.Fatal("expected telemetry disabled")
	}
	res, err := ReportImpact(summary(1, 100, 50, 50, nil))
	if err != nil {
		t.Fatalf("ReportImpact disabled: %v", err)
	}
	if !res.Disabled {
		t.Fatalf("disabled result = %#v", res)
	}
}

func TestRecordCommandFlushesAfterInterval(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envEnabled, "1")
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
	t.Setenv(envEnabled, "1")
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
	t.Setenv(envEnabled, "1")
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

func TestReportImpactClearsPendingAfterManualFlush(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envEnabled, "1")
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
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
	t.Setenv(envEnabled, "1")
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
