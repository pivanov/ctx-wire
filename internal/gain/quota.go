package gain

import (
	"fmt"
	"strings"

	"ctx-wire/internal/ui"
)

// DefaultContextWindow is the token size ctx-wire frames savings against when no
// monthly budget is configured. It makes a savings figure legible ("saved N
// full context windows") without tying the number to any vendor's pricing tier.
// 200K is a representative large-model window; it is purely a unit of scale.
const DefaultContextWindow = 200_000

// QuotaReport summarizes a period's token savings, optionally against a
// configured monthly budget, plus a per-agent split. It is deliberately
// vendor-neutral: everything is tokens, never dollars or subscription tiers.
type QuotaReport struct {
	Period        string      `json:"period"` // human label, e.g. "2026-06"
	Commands      int         `json:"commands"`
	SavedBytes    int64       `json:"saved_bytes"`
	SavedTokens   int64       `json:"saved_tokens"`
	BudgetTokens  int64       `json:"budget_tokens"`  // 0 when no budget is configured
	ContextWindow int64       `json:"context_window"` // tokens per context-window unit
	ByAgent       []AgentStat `json:"by_agent"`
}

// BudgetPct is the share of the monthly budget covered by savings (0 when no
// budget is set). It can exceed 100 when savings outrun the budget.
func (q QuotaReport) BudgetPct() float64 {
	if q.BudgetTokens <= 0 {
		return 0
	}
	return float64(q.SavedTokens) / float64(q.BudgetTokens) * 100
}

// Windows is the savings expressed as a multiple of one context window.
func (q QuotaReport) Windows() float64 {
	if q.ContextWindow <= 0 {
		return 0
	}
	return float64(q.SavedTokens) / float64(q.ContextWindow)
}

// Quota builds a QuotaReport from entries already filtered to the period. period
// is the display label; budgetTokens (0 = none) and contextWindow (0 = default)
// come from config or flags.
func Quota(entries []Entry, period string, budgetTokens, contextWindow int64) QuotaReport {
	if contextWindow <= 0 {
		contextWindow = DefaultContextWindow
	}
	var savedBytes int64
	for _, e := range entries {
		savedBytes += int64(e.SavedBytes)
	}
	return QuotaReport{
		Period:        period,
		Commands:      len(entries),
		SavedBytes:    savedBytes,
		SavedTokens:   approxTokens(savedBytes),
		BudgetTokens:  budgetTokens,
		ContextWindow: contextWindow,
		ByAgent:       AgentTotals(entries),
	}
}

// FormatQuotaThemed renders a quota report: savings for the period, a
// context-window framing, an optional budget meter, and a per-agent split.
func FormatQuotaThemed(q QuotaReport, theme ui.Theme) string {
	gt := gainTheme{Theme: theme}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", gt.Heading("ctx-wire gain: quota ("+q.Period+")"))
	if q.Commands == 0 {
		b.WriteString("no commands recorded this period yet\n")
		return b.String()
	}

	b.WriteByte('\n')
	fmt.Fprintf(&b, "%s   %s\n", gt.Field("Saved this period",
		gt.Number.Render(humanTokens(q.SavedTokens)+" tokens")),
		gt.Dim.Render(fmt.Sprintf("(%d commands)", q.Commands)))
	fmt.Fprintf(&b, "%s\n", gt.Field("Context windows",
		gt.Number.Render(fmt.Sprintf("%.1f saved", q.Windows()))+
			gt.Dim.Render(fmt.Sprintf(" (%s tokens each)", humanTokensPlain(q.ContextWindow)))))

	if q.BudgetTokens > 0 {
		fmt.Fprintf(&b, "%s / %s\n", gt.Field("Monthly budget",
			gt.Number.Render(humanTokens(q.SavedTokens))),
			gt.Number.Render(humanTokens(q.BudgetTokens)+" tokens"))
		fmt.Fprintf(&b, "%s %s\n", gt.Field("Budget meter",
			gt.bar(q.BudgetPct(), 28)), gt.percent(q.BudgetPct()))
		if q.BudgetPct() >= 100 {
			fmt.Fprintf(&b, "%s\n", gt.Good.Render(
				fmt.Sprintf("savings cover this month's budget %.1fx over", q.BudgetPct()/100)))
		}
	}

	agents := attributedAgents(q.ByAgent)
	if len(agents) > 0 {
		fmt.Fprintf(&b, "\n%s\n", gt.Section.Render("By Agent"))
		for _, a := range agents {
			name := a.Agent
			if name == "" {
				name = "unattributed"
			}
			pct := 0.0
			if q.SavedBytes > 0 {
				pct = float64(a.SavedBytes) / float64(q.SavedBytes) * 100
			}
			fmt.Fprintf(&b, "  %-14s %10s  %s\n",
				gt.Command.Render(name),
				gt.Number.Render(humanTokens(approxTokens(a.SavedBytes))),
				gt.percentBare(pct))
		}
	}
	return b.String()
}

// attributedAgents returns the agent buckets worth showing in the quota split:
// any bucket with positive savings, including the unattributed one (labeled at
// render time) so the picture is honest about coverage.
func attributedAgents(stats []AgentStat) []AgentStat {
	out := make([]AgentStat, 0, len(stats))
	for _, s := range stats {
		if s.SavedBytes > 0 {
			out = append(out, s)
		}
	}
	return out
}

// humanTokensPlain formats a token count without the leading "~", for unit
// labels like the context-window size.
func humanTokensPlain(n int64) string {
	return strings.TrimPrefix(humanTokens(n), "~")
}
