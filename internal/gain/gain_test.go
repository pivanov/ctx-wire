package gain

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func useTempLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv(envFile, path)
	t.Setenv(envDisable, "")
	return path
}

func TestRecordAppends(t *testing.T) {
	path := useTempLog(t)
	for i := 0; i < 3; i++ {
		if err := Record("git status", 1000, 100, 0); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 appended lines, got %d", len(lines))
	}
	var e Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("line not valid JSON: %v", err)
	}
	if e.RawBytes != 1000 || e.EmittedBytes != 100 || e.SavedBytes != 900 {
		t.Errorf("unexpected bytes: %+v", e)
	}
	if e.TS == "" {
		t.Error("expected a timestamp")
	}
}

func TestRecordRotatesAndReadsGainFamily(t *testing.T) {
	path := useTempLog(t)
	oldMax := maxGainLogSize
	maxGainLogSize = 180
	t.Cleanup(func() { maxGainLogSize = oldMax })

	for i := 0; i < 6; i++ {
		if err := Record("git status", 1000, 100, 0); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated gain log: %v", err)
	}
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Commands != 6 {
		t.Fatalf("summary commands = %d, want 6", s.Commands)
	}
	recent, err := RecentEntries(2)
	if err != nil {
		t.Fatalf("RecentEntries: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("recent len = %d, want 2", len(recent))
	}
	if err := Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	for _, p := range gainLogFamily(path) {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("Clear left gain log %s", p)
		}
	}
}

func TestRecordScrubsCommand(t *testing.T) {
	path := useTempLog(t)
	if err := Record("curl -H 'Authorization: Bearer sk-secrettoken12345'", 500, 50, 0); err != nil {
		t.Fatalf("Record: %v", err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "sk-secrettoken12345") {
		t.Errorf("secret leaked into gain log: %s", data)
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Errorf("expected redaction marker in gain log: %s", data)
	}
}

func TestRecordWithMetaStoresFilterPath(t *testing.T) {
	path := useTempLog(t)
	if err := RecordWithMeta("git status", "git-status", "filtered", "", "", 1000, 100, 0); err != nil {
		t.Fatalf("RecordWithMeta: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &e); err != nil {
		t.Fatalf("line not valid JSON: %v", err)
	}
	if e.Filter != "git-status" || e.Mode != "filtered" {
		t.Fatalf("metadata = filter %q mode %q", e.Filter, e.Mode)
	}
}

func TestRecordWithMetaStoresAgent(t *testing.T) {
	path := useTempLog(t)
	if err := RecordWithMeta("git status", "git-status", "filtered", "claude", "hook", 1000, 100, 0); err != nil {
		t.Fatalf("RecordWithMeta: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &e); err != nil {
		t.Fatalf("line not valid JSON: %v", err)
	}
	if e.Agent != "claude" {
		t.Fatalf("agent = %q, want %q", e.Agent, "claude")
	}
	if e.Source != "hook" {
		t.Fatalf("source = %q, want %q (hook/shim/run lets us compare savings by entry point)", e.Source, "hook")
	}
}

func TestRecordOmitsEmptyAgent(t *testing.T) {
	path := useTempLog(t)
	if err := Record("git status", 1000, 100, 0); err != nil {
		t.Fatalf("Record: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"agent"`) {
		t.Errorf("unattributed entry should omit the agent field: %s", data)
	}
}

func TestAgentTotals(t *testing.T) {
	entries := []Entry{
		{Agent: "claude", RawBytes: 1000, EmittedBytes: 400, SavedBytes: 600},
		{Agent: "codex", RawBytes: 1000, EmittedBytes: 600, SavedBytes: 400},
		{Agent: "claude", RawBytes: 500, EmittedBytes: 100, SavedBytes: 400},
		{Agent: "", RawBytes: 9999, EmittedBytes: 1, SavedBytes: 9998}, // unattributed, biggest
	}
	got := AgentTotals(entries)
	if len(got) != 3 {
		t.Fatalf("got %d buckets, want 3: %+v", len(got), got)
	}
	// claude first (1000 saved across 2 commands), then codex, then unattributed
	// last despite being the largest single bucket.
	if got[0].Agent != "claude" || got[0].Commands != 2 || got[0].SavedBytes != 1000 {
		t.Errorf("bucket[0] = %+v, want claude/2/1000", got[0])
	}
	if got[1].Agent != "codex" || got[1].SavedBytes != 400 {
		t.Errorf("bucket[1] = %+v, want codex/400", got[1])
	}
	if got[2].Agent != "" {
		t.Errorf("bucket[2] = %+v, want unattributed last", got[2])
	}
	if pct := got[0].SavingsPct(); pct < 66.6 || pct > 66.7 {
		t.Errorf("claude SavingsPct = %.2f, want ~66.67", pct)
	}
}

func TestSummarizeByAgent(t *testing.T) {
	useTempLog(t)
	// claude: 800 + 100 saved across two commands; codex: 400; unattributed: 8999.
	mustAgentRecord(t, "git status", "claude", 1000, 200)
	mustAgentRecord(t, "go test ./...", "codex", 500, 100)
	mustAgentRecord(t, "git log", "claude", 200, 100)
	mustAgentRecord(t, "make build", "", 9000, 1)

	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.ByAgent) != 3 {
		t.Fatalf("ByAgent buckets = %d, want 3: %+v", len(s.ByAgent), s.ByAgent)
	}
	if s.ByAgent[0].Agent != "claude" || s.ByAgent[0].SavedBytes != 900 || s.ByAgent[0].Commands != 2 {
		t.Errorf("bucket[0] = %+v, want claude/900/2", s.ByAgent[0])
	}
	if s.ByAgent[1].Agent != "codex" || s.ByAgent[1].SavedBytes != 400 {
		t.Errorf("bucket[1] = %+v, want codex/400", s.ByAgent[1])
	}
	// Unattributed sinks last despite being the largest single bucket.
	if s.ByAgent[2].Agent != "" || s.ByAgent[2].SavedBytes != 8999 {
		t.Errorf("bucket[2] = %+v, want unattributed/8999 last", s.ByAgent[2])
	}
}

func mustAgentRecord(t *testing.T, cmd, agentName string, raw, emitted int) {
	t.Helper()
	if err := RecordWithMeta(cmd, "", "filtered", agentName, "", raw, emitted, 0); err != nil {
		t.Fatalf("RecordWithMeta: %v", err)
	}
}

func TestRecordDisabled(t *testing.T) {
	path := useTempLog(t)
	t.Setenv(envDisable, "0")
	if err := Record("git status", 100, 10, 0); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("expected no log file when CTX_WIRE_GAIN=0")
	}
}

func TestRecordFallsBackWhenPrimaryUnavailable(t *testing.T) {
	dir := t.TempDir()
	primaryBase := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(primaryBase, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(dir, "fallback", "gain.jsonl")
	t.Setenv(envFile, "")
	t.Setenv(envFallbackFile, fallback)
	t.Setenv(envDisable, "")
	t.Setenv("XDG_DATA_HOME", primaryBase)

	if err := Record("git status", 1000, 100, 0); err != nil {
		t.Fatalf("Record: %v", err)
	}
	data, err := os.ReadFile(fallback)
	if err != nil {
		t.Fatalf("expected fallback log: %v", err)
	}
	if !strings.Contains(string(data), `"command":"git status"`) {
		t.Fatalf("fallback log missing command: %s", data)
	}

	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Commands != 1 || s.SavedBytes != 900 {
		t.Fatalf("fallback summary wrong: %+v", s)
	}
}

func TestSummarizeAggregates(t *testing.T) {
	useTempLog(t)
	mustRecord(t, "git status", 1000, 100, 0)
	mustRecord(t, "git diff", 2000, 200, 0)
	mustRecord(t, "dotnet build", 5000, 50, 0)
	mustRecord(t, "/usr/bin/dotnet test", 1000, 100, 0)
	mustRecord(t, "ctx-wire gain", 10_000, 10_000, 0)

	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Commands != 4 {
		t.Errorf("commands = %d, want 4", s.Commands)
	}
	if s.RawBytes != 9000 || s.EmittedBytes != 450 || s.SavedBytes != 8550 {
		t.Errorf("totals wrong: %+v", s)
	}
	// Grouped by program: git (2 cmds) and dotnet (2, including /usr/bin/dotnet).
	byProg := map[string]CommandStat{}
	for _, st := range s.ByProgram {
		byProg[st.Program] = st
	}
	if byProg["git"].Count != 2 {
		t.Errorf("git count = %d, want 2", byProg["git"].Count)
	}
	if byProg["dotnet"].Count != 2 {
		t.Errorf("dotnet count = %d, want 2", byProg["dotnet"].Count)
	}
	if _, ok := byProg["/usr/bin/dotnet"]; ok {
		t.Error("full-path program should be grouped by basename")
	}
	if _, ok := byProg["ctx-wire"]; ok {
		t.Error("ctx-wire self rows should be excluded from program summary")
	}
	// dotnet saved the most, so it sorts first.
	if len(s.ByProgram) == 0 || s.ByProgram[0].Program != "dotnet" {
		t.Errorf("expected dotnet first by savings, got %+v", s.ByProgram)
	}
}

func TestSummarizeOpportunities(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "cat README.md", "", "passthrough", 3000, 3000, 0)    // passthrough payload, >1KB -> opportunity
	mustRecordMeta(t, "rg TODO .", "rg", "filtered", 2000, 1950, 0)         // payload filtered, <32KB -> NOT
	mustRecordMeta(t, "rg --json big", "rg", "filtered", 50000, 49000, 0)   // payload filtered, >32KB -> opportunity
	mustRecordMeta(t, "go test ./...", "go", "filtered", 6000, 5800, 0)     // tooling filtered, low savings -> opportunity at normal floor
	mustRecordMeta(t, "git status", "git-status", "filtered", 1000, 100, 0) // below 1KB floor -> NOT

	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	byProg := map[string]OpportunityStat{}
	for _, o := range s.Opportunities {
		byProg[o.Program] = o
	}
	if len(s.Opportunities) != 3 {
		t.Fatalf("opportunities = %+v, want 3 (cat, rg-large, go)", s.Opportunities)
	}
	if _, ok := byProg["cat"]; !ok {
		t.Error("expected passthrough cat to be an opportunity")
	}
	if _, ok := byProg["go"]; !ok {
		t.Error("expected tooling go filtered low-savings to be an opportunity")
	}
	rg, ok := byProg["rg"]
	if !ok {
		t.Fatal("expected large payload rg to be an opportunity")
	}
	if rg.Count != 1 || rg.EmittedBytes != 49000 {
		t.Errorf("rg opportunity = %+v; small payload rg row should be excluded", rg)
	}
	// Highest emitted sorts first.
	if s.Opportunities[0].Program != "rg" {
		t.Errorf("top opportunity = %s, want rg (highest emitted)", s.Opportunities[0].Program)
	}
}

func TestPayloadFilteredBelowThresholdNotOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "sed -n 1,50p f", "sed", "filtered", 4000, 3950, 0) // payload, 3.95KB, low savings
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 0 {
		t.Fatalf("small payload filtered row should not be an opportunity: %+v", s.Opportunities)
	}
}

func TestPayloadPassthroughStillOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "sed -n 1,50p f", "", "passthrough", 4000, 4000, 0) // payload passthrough, 4KB
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 1 || s.Opportunities[0].Program != "sed" {
		t.Fatalf("payload passthrough should still surface: %+v", s.Opportunities)
	}
}

// TestPayloadFilterBelowThresholdNotOpportunity covers the dogfood signal: a
// small filtered git-status (and other payload filters / awk line reads) is
// expected payload, not actionable, so it must not clutter the gain table.
func TestPayloadFilterBelowThresholdNotOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "git status --short --untracked-files=all", "git-status", "filtered", 3000, 2950, 0) // ~3KB
	mustRecordMeta(t, "git diff -- README.md", "git-diff", "filtered", 4000, 3950, 0)                      // ~4KB
	mustRecordMeta(t, "awk 'NR>=507 && NR<=620' path/file.cs", "awk", "filtered", 3000, 2950, 0)           // awk line read
	mustRecordMeta(t, `awk '/Foo|Bar/{print NR": "$0}' path/file.cs`, "awk", "filtered", 3000, 2950, 0)    // awk search read
	mustRecordMeta(t, `awk 'NR>=1 && NR<=180 {printf "%6d\t%s\n", NR, $0}' file.css`, "awk", "filtered", 3000, 2950, 0)
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 0 {
		t.Fatalf("small payload-filter / awk-read rows should not be opportunities: %+v", s.Opportunities)
	}
}

// TestLargePayloadFilterStillOpportunity proves the higher floor still surfaces
// genuinely large payload reads.
func TestLargePayloadFilterStillOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "git status --short", "git-status", "filtered", 50000, 49000, 0) // >32KiB
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 1 || s.Opportunities[0].Filter != "git-status" {
		t.Fatalf("large git-status should still surface: %+v", s.Opportunities)
	}
}

func TestModeratePayloadFilterNotOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "tail /tmp/app.log", "tail", "filtered", 13000, 13042, 0) // ~12.7KiB, expected payload
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 0 {
		t.Fatalf("moderate payload reads should not surface as opportunities: %+v", s.Opportunities)
	}
}

func TestOpportunityPolicyDogfoodCases(t *testing.T) {
	cases := []struct {
		name       string
		entry      Entry
		wantKind   OpportunityKind
		wantInc    bool
		wantAction bool
		wantHint   string
	}{
		{
			name: "small git status hidden",
			entry: Entry{Command: "git status --short", Filter: "git-status", Mode: "filtered",
				RawBytes: 3000, EmittedBytes: 2950, SavedBytes: 0},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "moderate tail hidden",
			entry: Entry{Command: "tail /tmp/app.log", Filter: "tail", Mode: "filtered",
				RawBytes: 13000, EmittedBytes: 13042, SavedBytes: -42},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "full tee log shown with hint",
			entry: Entry{Command: "cat /Users/me/.local/share/ctx-wire/tee/178_head.log", Filter: "cat", Mode: "filtered",
				RawBytes: 12000, EmittedBytes: 12000, SavedBytes: 0},
			wantKind:   OpportunityExpectedPayload,
			wantInc:    true,
			wantAction: true,
			wantHint:   "full ctx-wire spool log",
		},
		{
			name: "missing filter shown",
			entry: Entry{Command: "lsof -i :7222 -n -P", Mode: "passthrough",
				RawBytes: 3000, EmittedBytes: 3000, SavedBytes: 0},
			wantKind:   OpportunityMissingFilter,
			wantInc:    true,
			wantAction: true,
		},
		{
			name: "weak tooling shown",
			entry: Entry{Command: "webpack build", Filter: "webpack", Mode: "filtered",
				RawBytes: 3000, EmittedBytes: 2900, SavedBytes: 100},
			wantKind:   OpportunityWeakFilter,
			wantInc:    true,
			wantAction: true,
		},
		{
			name: "large search without line numbers shown with hint",
			entry: Entry{Command: "rg fetchData packages", Filter: "rg", Mode: "filtered",
				RawBytes: 14000, EmittedBytes: 14000, SavedBytes: 0},
			wantKind:   OpportunityExpectedPayload,
			wantInc:    true,
			wantAction: true,
			wantHint:   "add -n/--line-number",
		},
		{
			name: "large search with line numbers hidden under payload floor",
			entry: Entry{Command: "rg -n fetchData packages", Filter: "rg", Mode: "filtered",
				RawBytes: 14000, EmittedBytes: 14000, SavedBytes: 0},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "sort is conservative payload",
			entry: Entry{Command: "sort -u big.txt", Filter: "sort", Mode: "filtered",
				RawBytes: 13000, EmittedBytes: 12950, SavedBytes: 50},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "tr is conservative payload",
			entry: Entry{Command: "tr a-z A-Z", Filter: "tr", Mode: "filtered",
				RawBytes: 13000, EmittedBytes: 12950, SavedBytes: 50},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "cut is conservative payload",
			entry: Entry{Command: "cut -f2 -d, data.csv", Filter: "cut", Mode: "filtered",
				RawBytes: 13000, EmittedBytes: 12950, SavedBytes: 50},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "base64 is conservative payload",
			entry: Entry{Command: "base64 payload.bin", Filter: "base64", Mode: "filtered",
				RawBytes: 13000, EmittedBytes: 12950, SavedBytes: 50},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "xargs is conservative payload",
			entry: Entry{Command: "xargs grep TODO", Filter: "xargs", Mode: "filtered",
				RawBytes: 13000, EmittedBytes: 12950, SavedBytes: 50},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "awk field extraction is conservative payload",
			entry: Entry{Command: "awk '{print $2}' access.log", Filter: "awk", Mode: "filtered",
				RawBytes: 13000, EmittedBytes: 12950, SavedBytes: 50},
			wantKind: OpportunityExpectedPayload,
		},
		{
			name: "awk aggregation stays a weak filter",
			entry: Entry{Command: "awk '{sum+=$3} END {print sum}' data.csv", Filter: "awk", Mode: "filtered",
				RawBytes: 13000, EmittedBytes: 12500, SavedBytes: 500},
			wantKind:   OpportunityWeakFilter,
			wantInc:    true,
			wantAction: true,
		},
		{
			name: "hook limitation carried but not actionable",
			entry: Entry{Command: "cat app.log | grep err", Mode: "passthrough",
				RawBytes: 12000, EmittedBytes: 12000, SavedBytes: 0},
			wantKind: OpportunityHookLimited,
			wantInc:  true,
		},
		{
			name: "shared interactive bypass list",
			entry: Entry{Command: "tmux new-session", Mode: "passthrough",
				RawBytes: 3000, EmittedBytes: 3000, SavedBytes: 0},
			wantKind: OpportunityHookLimited,
			wantInc:  true,
		},
	}
	for _, tt := range cases {
		got := OpportunityPolicyForEntry(tt.entry, 0)
		if got.Kind != tt.wantKind || got.Include != tt.wantInc || got.Actionable != tt.wantAction {
			t.Fatalf("%s: policy = %+v, want kind=%s include=%v actionable=%v", tt.name, got, tt.wantKind, tt.wantInc, tt.wantAction)
		}
		if tt.wantHint != "" && !strings.Contains(got.Hint, tt.wantHint) {
			t.Fatalf("%s: hint = %q, want containing %q", tt.name, got.Hint, tt.wantHint)
		}
	}
}

// TestWeakToolingFilterStillOpportunity proves non-payload tooling filters keep
// the low floor (a real, actionable gap).
func TestWeakToolingFilterStillOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "webpack build", "webpack", "filtered", 3000, 2900, 0) // not payload, ~3KB, low savings
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 1 || s.Opportunities[0].Program != "webpack" {
		t.Fatalf("weak tooling filter should still surface: %+v", s.Opportunities)
	}
}

func TestInlineScriptBelowThresholdNotOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "bun -e 'console.log(1)'", "inline-script", "filtered", 12000, 11800, 0)
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 0 {
		t.Fatalf("inline script payload should not surface below payload floor: %+v", s.Opportunities)
	}
}

func TestShellScriptBelowThresholdNotOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "bash /tmp/inv.sh", "shell-script", "filtered", 12000, 11800, 0)
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 0 {
		t.Fatalf("shell script payload should not surface below payload floor: %+v", s.Opportunities)
	}
}

// TestPassthroughMissingFilterStillOpportunity proves passthrough gaps keep the
// low floor regardless of program.
func TestPassthroughMissingFilterStillOpportunity(t *testing.T) {
	useTempLog(t)
	mustRecordMeta(t, "frobnicate --all", "", "passthrough", 3000, 3000, 0)
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.Opportunities) != 1 || s.Opportunities[0].Program != "frobnicate" {
		t.Fatalf("passthrough missing-filter should still surface: %+v", s.Opportunities)
	}
}

func TestSummarizeSince(t *testing.T) {
	path := useTempLog(t)
	old := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().UTC().Format(time.RFC3339)
	writeEntry(t, path, Entry{TS: old, Command: "git status", RawBytes: 1000, EmittedBytes: 100, SavedBytes: 900})
	writeEntry(t, path, Entry{TS: recent, Command: "rg TODO .", RawBytes: 2000, EmittedBytes: 1000, SavedBytes: 1000})

	s, err := SummarizeWithOptions(Options{Since: time.Now().Add(-30 * time.Minute)})
	if err != nil {
		t.Fatalf("SummarizeWithOptions: %v", err)
	}
	if s.Commands != 1 || s.RawBytes != 2000 || s.SavedBytes != 1000 {
		t.Fatalf("since summary wrong: %+v", s)
	}
}

func TestClearRemovesLogs(t *testing.T) {
	path := useTempLog(t)
	mustRecord(t, "git status", 1000, 100, 0)
	if err := Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("expected primary log removed")
	}
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Commands != 0 {
		t.Fatalf("expected empty summary after clear, got %+v", s)
	}
}

func TestSummarizeMergesPrimaryAndFallback(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "data", "ctx-wire", "gain.jsonl")
	fallback := filepath.Join(dir, "fallback", "gain.jsonl")
	t.Setenv(envFile, "")
	t.Setenv(envFallbackFile, fallback)
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))

	writeEntry(t, primary, Entry{Command: "git status", RawBytes: 1000, EmittedBytes: 100, SavedBytes: 900})
	writeEntry(t, fallback, Entry{Command: "df -h", RawBytes: 500, EmittedBytes: 300, SavedBytes: 200})

	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Commands != 2 || s.RawBytes != 1500 || s.EmittedBytes != 400 || s.SavedBytes != 1100 {
		t.Fatalf("merged summary wrong: %+v", s)
	}
	byProg := map[string]CommandStat{}
	for _, st := range s.ByProgram {
		byProg[st.Program] = st
	}
	if byProg["git"].SavedBytes != 900 || byProg["df"].SavedBytes != 200 {
		t.Fatalf("merged programs wrong: %+v", s.ByProgram)
	}
}

// TestEntriesSortsChronologicallyAcrossStores guards the --history --top bug:
// the fallback store is read after the primary, so a newer entry in the primary
// and an older one in the fallback must still come back in timestamp order (not
// store order), or --history --top N would surface the wrong entries.
func TestEntriesSortsChronologicallyAcrossStores(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "data", "ctx-wire", "gain.jsonl")
	fallback := filepath.Join(dir, "fallback", "gain.jsonl")
	t.Setenv(envFile, "")
	t.Setenv(envFallbackFile, fallback)
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))

	// Newer entry in the primary (read first), older entry in the fallback.
	writeEntry(t, primary, Entry{TS: "2026-06-05T10:00:00Z", Command: "rg recent", RawBytes: 2000, EmittedBytes: 1000, SavedBytes: 1000})
	writeEntry(t, fallback, Entry{TS: "2026-06-01T10:00:00Z", Command: "git old", RawBytes: 1000, EmittedBytes: 100, SavedBytes: 900})

	entries, err := Entries(Options{})
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	// Oldest first, newest last (chronological), regardless of store order.
	if !strings.Contains(entries[0].Command, "git old") {
		t.Errorf("entries[0] = %q, want the older fallback entry first", entries[0].Command)
	}
	if !strings.Contains(entries[1].Command, "rg recent") {
		t.Errorf("entries[1] = %q, want the newer primary entry last", entries[1].Command)
	}
}

func TestSummarizeMissingLog(t *testing.T) {
	t.Setenv(envFile, filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Commands != 0 {
		t.Errorf("expected empty summary, got %+v", s)
	}
}

func TestSummarizeSkipsMalformedLines(t *testing.T) {
	path := useTempLog(t)
	mustRecord(t, "git status", 1000, 100, 0)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("this is not json\n")
	f.Close()
	mustRecord(t, "git diff", 500, 50, 0)

	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Commands != 2 {
		t.Errorf("expected 2 valid commands (malformed skipped), got %d", s.Commands)
	}
}

func TestRecordCapsCommandSample(t *testing.T) {
	path := useTempLog(t)
	huge := "bun -e " + strings.Repeat("x", maxCommandSampleBytes*2)
	mustRecord(t, huge, 1000, 100, 0)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal(bytes.TrimSpace(data), &e); err != nil {
		t.Fatal(err)
	}
	if len(e.Command) > maxCommandSampleBytes {
		t.Fatalf("command sample length = %d, want <= %d", len(e.Command), maxCommandSampleBytes)
	}
	if !strings.Contains(e.Command, "[truncated]") {
		t.Fatalf("truncated command missing marker: %q", e.Command)
	}
}

func TestSummarizeSkipsOversizeLines(t *testing.T) {
	path := useTempLog(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(strings.Repeat("x", maxGainLineBytes+1024) + "\n"); err != nil {
		t.Fatal(err)
	}
	valid, _ := json.Marshal(Entry{TS: time.Now().UTC().Format(time.RFC3339), Command: "git status", RawBytes: 1000, EmittedBytes: 100, SavedBytes: 900})
	if _, err := f.Write(append(valid, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Commands != 1 || s.SavedBytes != 900 {
		t.Fatalf("summary after oversize line = %+v, want one valid entry", s)
	}
}

func TestFormatPlainHasNoANSI(t *testing.T) {
	s := &Summary{
		Commands:     2,
		RawBytes:     1000,
		EmittedBytes: 250,
		SavedBytes:   750,
		ByProgram: []CommandStat{
			{Program: "git", Count: 2, RawBytes: 1000, EmittedBytes: 250, SavedBytes: 750},
		},
	}

	out := Format(s)
	if !strings.Contains(out, "ctx-wire gain: summary") {
		t.Fatalf("Format should carry the report heading:\n%s", out)
	}
	for _, want := range []string{"Saved tokens:", "Efficiency:", "By Program", "git"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Format missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("plain Format should not contain ANSI escapes:\n%q", out)
	}
}

func TestFormatStyledUsesANSI(t *testing.T) {
	s := &Summary{
		Commands:     1,
		RawBytes:     1000,
		EmittedBytes: 100,
		SavedBytes:   900,
		ByProgram: []CommandStat{
			{Program: "go", Count: 1, RawBytes: 1000, EmittedBytes: 100, SavedBytes: 900},
		},
	}

	out := FormatStyled(s, true)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("styled Format should contain ANSI escapes:\n%q", out)
	}
	if !strings.Contains(out, "Efficiency:") || !strings.Contains(out, "By Program") {
		t.Fatalf("styled Format missing report sections:\n%s", out)
	}
}

func TestFormatStyledNegativeImpactUsesBlockGlyph(t *testing.T) {
	s := &Summary{
		Commands:     2,
		RawBytes:     2000,
		EmittedBytes: 1500,
		SavedBytes:   500,
		ByProgram: []CommandStat{
			{Program: "good", Count: 1, RawBytes: 1000, EmittedBytes: 500, SavedBytes: 500},
			{Program: "bad", Count: 1, RawBytes: 1000, EmittedBytes: 1005, SavedBytes: -5},
		},
	}

	out := FormatStyled(s, true)
	if strings.Contains(out, "---") {
		t.Fatalf("negative impact should not use dashes:\n%s", out)
	}
	if !strings.Contains(out, "░░░") {
		t.Fatalf("negative impact should use block glyphs:\n%s", out)
	}
}

func mustRecord(t *testing.T, cmd string, raw, emitted, code int) {
	t.Helper()
	if err := Record(cmd, raw, emitted, code); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func mustRecordMeta(t *testing.T, cmd, filterName, mode string, raw, emitted, code int) {
	t.Helper()
	if err := RecordWithMeta(cmd, filterName, mode, "", "", raw, emitted, code); err != nil {
		t.Fatalf("RecordWithMeta: %v", err)
	}
}

func writeEntry(t *testing.T, path string, e Entry) {
	t.Helper()
	line, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
