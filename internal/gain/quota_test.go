package gain

import (
	"strings"
	"testing"

	"ctx-wire/internal/ui"
)

func TestQuotaBuildsReport(t *testing.T) {
	entries := []Entry{
		{Agent: "claude", RawBytes: 120000, EmittedBytes: 20000, SavedBytes: 100000},
		{Agent: "codex", RawBytes: 80000, EmittedBytes: 30000, SavedBytes: 50000},
		{Agent: "", RawBytes: 10000, EmittedBytes: 8000, SavedBytes: 2000},
	}
	q := Quota(entries, "2026-06", 500_000, 0)

	if q.Period != "2026-06" || q.Commands != 3 {
		t.Fatalf("period/commands = %q/%d", q.Period, q.Commands)
	}
	if q.SavedBytes != 152000 {
		t.Errorf("SavedBytes = %d, want 152000", q.SavedBytes)
	}
	if want := approxTokens(152000); q.SavedTokens != want {
		t.Errorf("SavedTokens = %d, want %d", q.SavedTokens, want)
	}
	if q.ContextWindow != DefaultContextWindow {
		t.Errorf("ContextWindow = %d, want default %d", q.ContextWindow, DefaultContextWindow)
	}
	if q.BudgetTokens != 500_000 {
		t.Errorf("BudgetTokens = %d, want 500000", q.BudgetTokens)
	}
	// claude leads the per-agent split; unattributed bucket is present and last.
	if len(q.ByAgent) != 3 || q.ByAgent[0].Agent != "claude" || q.ByAgent[2].Agent != "" {
		t.Fatalf("ByAgent order = %+v", q.ByAgent)
	}
	if pct := q.BudgetPct(); pct < 7.5 || pct > 7.7 {
		t.Errorf("BudgetPct = %.2f, want ~7.6", pct)
	}
}

func TestQuotaWindowsAndNoBudget(t *testing.T) {
	entries := []Entry{{Agent: "claude", RawBytes: 800000, EmittedBytes: 0, SavedBytes: 800000}}
	q := Quota(entries, "2026-06", 0, 200_000)
	if q.BudgetTokens != 0 || q.BudgetPct() != 0 {
		t.Errorf("expected no budget, got tokens=%d pct=%.2f", q.BudgetTokens, q.BudgetPct())
	}
	// 800000 bytes -> 200000 tokens -> exactly one 200K window.
	if w := q.Windows(); w < 0.99 || w > 1.01 {
		t.Errorf("Windows = %.3f, want ~1.0", w)
	}
}

func TestFormatQuotaThemed(t *testing.T) {
	theme := ui.New(false, false, nil)

	empty := FormatQuotaThemed(Quota(nil, "2026-06", 0, 0), theme)
	if !strings.Contains(empty, "no commands recorded") {
		t.Errorf("empty quota should say so, got:\n%s", empty)
	}

	q := Quota([]Entry{
		{Agent: "claude", RawBytes: 120000, SavedBytes: 100000},
		{Agent: "codex", RawBytes: 80000, SavedBytes: 60000},
	}, "2026-06", 500_000, 0)
	out := FormatQuotaThemed(q, theme)
	for _, want := range []string{
		"quota (2026-06)",
		"Saved this period:",
		"Context windows:",
		"Monthly budget:",
		"Budget meter:",
		"By Agent",
		"claude",
		"codex",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("quota output missing %q:\n%s", want, out)
		}
	}
	// No budget configured: the budget lines are omitted.
	noBudget := FormatQuotaThemed(Quota([]Entry{{Agent: "claude", SavedBytes: 1000, RawBytes: 2000}}, "2026-06", 0, 0), theme)
	if strings.Contains(noBudget, "Monthly budget:") {
		t.Errorf("budget-free quota should omit the budget line:\n%s", noBudget)
	}
}
