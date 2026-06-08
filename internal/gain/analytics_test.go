package gain

import (
	"encoding/json"
	"strings"
	"testing"

	"ctx-wire/internal/ui"
)

func mkEntry(ts, cmd string, raw, emitted, saved int) Entry {
	return Entry{TS: ts, Command: cmd, RawBytes: raw, EmittedBytes: emitted, SavedBytes: saved}
}

func TestDailyGroupsByUTCDate(t *testing.T) {
	entries := []Entry{
		mkEntry("2026-06-03T23:00:00Z", "rg x", 1000, 100, 900),
		mkEntry("2026-06-04T01:00:00Z", "cat y", 500, 250, 250),
		mkEntry("2026-06-04T10:00:00Z", "git status", 200, 100, 100),
		mkEntry("not-a-time", "skip me", 999, 0, 999), // unparseable → dropped
	}
	daily := Daily(entries)
	if len(daily) != 2 {
		t.Fatalf("want 2 days, got %d: %+v", len(daily), daily)
	}
	if daily[0].Date != "2026-06-03" || daily[1].Date != "2026-06-04" {
		t.Fatalf("days not sorted oldest-first: %+v", daily)
	}
	if daily[0].Commands != 1 || daily[0].SavedBytes != 900 {
		t.Errorf("day 1 wrong: %+v", daily[0])
	}
	if daily[1].Commands != 2 || daily[1].SavedBytes != 350 || daily[1].RawBytes != 700 {
		t.Errorf("day 2 wrong: %+v", daily[1])
	}
	if got := daily[1].SavingsPct(); got < 49.9 || got > 50.1 {
		t.Errorf("day 2 savings pct = %.2f, want ~50", got)
	}
}

func TestWeeklyAndMonthlyGrouping(t *testing.T) {
	entries := []Entry{
		mkEntry("2026-05-30T10:00:00Z", "a", 100, 10, 90), // 2026-W22, 2026-05
		mkEntry("2026-06-01T10:00:00Z", "b", 100, 10, 90), // 2026-W23, 2026-06
		mkEntry("2026-06-03T10:00:00Z", "c", 100, 10, 90), // 2026-W23, 2026-06
	}
	wk := Weekly(entries)
	if len(wk) != 2 || wk[0].Date != "2026-W22" || wk[1].Date != "2026-W23" {
		t.Fatalf("weekly = %+v", wk)
	}
	if wk[1].Commands != 2 || wk[1].SavedBytes != 180 {
		t.Errorf("week 23 wrong: %+v", wk[1])
	}
	mo := Monthly(entries)
	if len(mo) != 2 || mo[0].Date != "2026-05" || mo[1].Date != "2026-06" {
		t.Fatalf("monthly = %+v", mo)
	}
	if mo[1].Commands != 2 {
		t.Errorf("june wrong: %+v", mo[1])
	}
}

func TestFormatHistoryLimitNewestFirst(t *testing.T) {
	entries := []Entry{
		mkEntry("2026-06-04T01:00:00Z", "rg old", 100, 10, 90),
		mkEntry("2026-06-04T02:00:00Z", "cat mid", 100, 10, 90),
		mkEntry("2026-06-04T03:00:00Z", "git new", 100, 10, 90),
	}
	out := FormatHistoryThemed(entries, 2, ui.Plain())
	// limit=2 → only the two newest; newest ("git") appears before "cat".
	if strings.Contains(out, "rg") {
		t.Errorf("oldest entry should be dropped by limit:\n%s", out)
	}
	gi, ci := strings.Index(out, "git"), strings.Index(out, "cat")
	if gi < 0 || ci < 0 || gi > ci {
		t.Errorf("history should be newest-first (git before cat):\n%s", out)
	}
}

func TestFormatHistoryShowsAgentAndCommand(t *testing.T) {
	entries := []Entry{
		{TS: "2026-06-04T03:00:00Z", Command: "git status --porcelain", Agent: "cursor", SavedBytes: 42},
	}
	out := FormatHistoryThemed(entries, 10, ui.Plain())
	if !strings.Contains(out, "cursor") {
		t.Errorf("history should show the invoking agent:\n%s", out)
	}
	if !strings.Contains(out, "git status --porcelain") {
		t.Errorf("history should show the full command:\n%s", out)
	}
}

func TestClipHistoryCommand(t *testing.T) {
	if got := clipHistoryCommand("a\nb\tc"); got != "a b c" {
		t.Errorf("newlines/tabs should collapse to spaces, got %q", got)
	}
	got := clipHistoryCommand(strings.Repeat("x", 200))
	if r := []rune(got); len(r) != 100 || !strings.HasSuffix(got, "…") {
		t.Errorf("a long command should clip to 100 runes with an ellipsis, got len=%d", len(r))
	}
}

func TestFormatJSONShape(t *testing.T) {
	s := &Summary{
		Commands: 3, RawBytes: 1700, EmittedBytes: 450, SavedBytes: 1250,
		ByProgram: []CommandStat{{Program: "rg", Count: 1, RawBytes: 1000, SavedBytes: 900}},
	}
	daily := []DailyStat{{Date: "2026-06-04", Commands: 3, RawBytes: 1700, SavedBytes: 1250}}
	out, err := FormatJSON(s, daily)
	if err != nil {
		t.Fatal(err)
	}
	var parsed jsonExport
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if parsed.Commands != 3 || parsed.SavedBytes != 1250 || len(parsed.ByProgram) != 1 || len(parsed.Daily) != 1 {
		t.Errorf("json shape wrong: %+v", parsed)
	}
	if parsed.SavingsPct < 73 || parsed.SavingsPct > 74 {
		t.Errorf("savings pct = %.2f, want ~73.5", parsed.SavingsPct)
	}
}

func TestFormatCSVHeaderAndRows(t *testing.T) {
	daily := []DailyStat{
		{Date: "2026-06-03", Commands: 1, RawBytes: 1000, EmittedBytes: 100, SavedBytes: 900},
		{Date: "2026-06-04", Commands: 2, RawBytes: 700, EmittedBytes: 350, SavedBytes: 350},
	}
	out := FormatCSV(daily)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[0] != "date,commands,raw_bytes,emitted_bytes,saved_bytes,savings_pct" {
		t.Errorf("bad header: %q", lines[0])
	}
	if len(lines) != 3 {
		t.Fatalf("want header + 2 rows, got %d lines", len(lines))
	}
	if lines[1] != "2026-06-03,1,1000,100,900,90.0" {
		t.Errorf("row 1 = %q", lines[1])
	}
}
