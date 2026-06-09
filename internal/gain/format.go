package gain

import (
	"fmt"
	"strings"

	"ctx-wire/internal/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// Format renders a human-readable summary report.
func Format(s *Summary) string {
	return FormatStyled(s, false)
}

// FormatStyled renders a human-readable summary report. When color is true,
// ANSI styling is used for terminal output; callers should disable it for pipes.
func FormatStyled(s *Summary, color bool) string {
	return FormatThemed(s, ui.New(color, true, nil))
}

// FormatThemed renders a human-readable summary report with the provided theme.
func FormatThemed(s *Summary, theme ui.Theme) string {
	var b strings.Builder
	gt := gainTheme{Theme: theme}
	fmt.Fprintf(&b, "%s\n", gt.Heading("ctx-wire gain: summary"))
	if s.Commands == 0 {
		fmt.Fprintf(&b, "\n%s\n", gt.Dim.Render("No commands recorded yet. Run commands through ctx-wire (or `ctx-wire init <agent>`) and check back."))
		return b.String()
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%s\n", gt.Field("Total commands", gt.Number.Render(fmt.Sprintf("%d", s.Commands))))
	fmt.Fprintf(&b, "%s\n", gt.Field("Raw bytes", gt.Number.Render(ui.HumanBytes(s.RawBytes))))
	fmt.Fprintf(&b, "%s\n", gt.Field("Emitted bytes", gt.Number.Render(ui.HumanBytes(s.EmittedBytes))))
	fmt.Fprintf(&b, "%s %s\n", gt.Field("Bytes saved", gt.Number.Render(ui.HumanBytes(s.SavedBytes))), gt.percent(s.SavingsPct()))
	fmt.Fprintf(&b, "%s\n", gt.Field("Saved tokens", gt.Number.Render(humanTokens(approxTokens(s.SavedBytes)))))
	fmt.Fprintf(&b, "%s %s\n", gt.Field("Efficiency", gt.bar(s.SavingsPct(), 28)), gt.percent(s.SavingsPct()))
	if len(s.ByProgram) > 0 {
		fmt.Fprintf(&b, "\n%s\n", gt.Section.Render("By Program"))
		maxSaved := maxProgramSaved(s.ByProgram)
		limit := min(len(s.ByProgram), 10)
		rows := make([][]string, 0, limit)
		for i := 0; i < limit; i++ {
			st := s.ByProgram[i]
			pct := programSavingsPct(st)
			rows = append(rows, []string{
				fmt.Sprintf("%d.", i+1),
				st.Program,
				fmt.Sprintf("%d", st.Count),
				ui.HumanBytes(st.SavedBytes),
				gt.percentBare(pct),
				gt.impact(st.SavedBytes, maxSaved, 18),
			})
		}
		b.WriteString(gt.table(rows))
		b.WriteByte('\n')
		if len(s.ByProgram) > limit {
			fmt.Fprintf(&b, "%s\n", gt.Dim.Render(fmt.Sprintf("... %d more programs", len(s.ByProgram)-limit)))
		}
	}
	if len(s.BySource) > 0 {
		fmt.Fprintf(&b, "\n%s\n", gt.Section.Render("By Source"))
		var maxSaved int64
		for _, st := range s.BySource {
			if st.SavedBytes > maxSaved {
				maxSaved = st.SavedBytes
			}
		}
		rows := make([][]string, 0, len(s.BySource))
		for _, st := range s.BySource {
			label := st.Source
			if label == "" {
				label = "(untagged)"
			}
			rows = append(rows, []string{
				label,
				fmt.Sprintf("%d", st.Commands),
				ui.HumanBytes(st.SavedBytes),
				gt.percentBare(st.SavingsPct()),
				gt.impact(st.SavedBytes, maxSaved, 18),
			})
		}
		b.WriteString(gt.sourceTable(rows))
		b.WriteByte('\n')
		fmt.Fprintf(&b, "%s\n", gt.Dim.Render("hook = agent rewrite/plugin, shim = PATH shim, run = manual ctx-wire run"))
	}
	if len(s.ByAgent) > 0 {
		fmt.Fprintf(&b, "\n%s\n", gt.Section.Render("Per Agent"))
		var maxSaved int64
		for _, st := range s.ByAgent {
			if st.SavedBytes > maxSaved {
				maxSaved = st.SavedBytes
			}
		}
		rows := make([][]string, 0, len(s.ByAgent))
		for _, st := range s.ByAgent {
			label := st.Agent
			if label == "" {
				label = "(untagged)"
			}
			rows = append(rows, []string{
				label,
				fmt.Sprintf("%d", st.Commands),
				ui.HumanBytes(st.SavedBytes),
				gt.percentBare(st.SavingsPct()),
				gt.impact(st.SavedBytes, maxSaved, 18),
			})
		}
		b.WriteString(gt.agentTable(rows))
		b.WriteByte('\n')
	}
	actionable := actionableOpportunities(s.Opportunities)
	if len(actionable) > 0 {
		fmt.Fprintf(&b, "\n%s\n", gt.Section.Render("Token Opportunities"))
		limit := min(len(actionable), 10)
		rows := make([][]string, 0, limit)
		for i := 0; i < limit; i++ {
			st := actionable[i]
			rows = append(rows, []string{
				fmt.Sprintf("%d.", i+1),
				st.Program,
				st.Mode,
				st.Filter,
				fmt.Sprintf("%d", st.Count),
				ui.HumanBytes(st.EmittedBytes),
				gt.percentBare(opportunitySavingsPct(st)),
			})
		}
		b.WriteString(gt.opportunityTable(rows))
		b.WriteByte('\n')
		if len(actionable) > limit {
			fmt.Fprintf(&b, "%s\n", gt.Dim.Render(fmt.Sprintf("... %d more opportunities", len(actionable)-limit)))
		}
	}
	return b.String()
}

func actionableOpportunities(rows []OpportunityStat) []OpportunityStat {
	out := make([]OpportunityStat, 0, len(rows))
	for _, row := range rows {
		if IsActionableOpportunity(row) {
			out = append(out, row)
		}
	}
	return out
}

func programSavingsPct(st CommandStat) float64 {
	if st.RawBytes == 0 {
		return 0
	}
	return float64(st.SavedBytes) / float64(st.RawBytes) * 100
}

func maxProgramSaved(stats []CommandStat) int64 {
	var max int64
	for _, st := range stats {
		if st.SavedBytes > max {
			max = st.SavedBytes
		}
	}
	return max
}

func opportunitySavingsPct(st OpportunityStat) float64 {
	if st.RawBytes == 0 {
		return 0
	}
	return float64(st.SavedBytes) / float64(st.RawBytes) * 100
}

type gainTheme struct {
	ui.Theme
}

func (t gainTheme) percent(v float64) string {
	s := fmt.Sprintf("(%.1f%%)", v)
	switch {
	case v >= 70:
		return t.Good.Render(s)
	case v >= 30:
		return t.Warn.Render(s)
	case v < 0:
		return t.Bad.Render(s)
	default:
		return t.Dim.Render(s)
	}
}

func (t gainTheme) percentBare(v float64) string {
	s := fmt.Sprintf("%.1f%%", v)
	switch {
	case v >= 70:
		return t.Good.Render(s)
	case v >= 30:
		return t.Warn.Render(s)
	case v < 0:
		return t.Bad.Render(s)
	default:
		return t.Dim.Render(s)
	}
}

// bar/filledBar/emptyBar delegate to the embedded ui.Theme so every CLI bar
// shares one fill implementation (ui.Theme.Meter).
func (t gainTheme) bar(pct float64, width int) string { return t.Bar(pct, width) }

func (t gainTheme) filledBar(width int) string { return t.Meter(width, 0) }

func (t gainTheme) emptyBar(width int) string { return t.Meter(0, width) }

// impact renders the relative-impact bar for a table's last column, using the
// same unbracketed fill as the efficiency and session-adoption bars.
func (t gainTheme) impact(saved, maxSaved int64, width int) string {
	var inner string
	switch {
	case saved < 0:
		negative := min(width, 3)
		inner = t.Bad.Render(strings.Repeat("░", negative)) + t.emptyBar(max(width-negative, 0))
	case maxSaved <= 0:
		inner = t.emptyBar(width)
	default:
		filled := int(float64(saved)/float64(maxSaved)*float64(width) + 0.5)
		if filled > width {
			filled = width
		}
		if !t.Color {
			inner = strings.Repeat("#", filled) + strings.Repeat(".", width-filled)
		} else {
			inner = t.filledBar(filled) + t.emptyBar(width-filled)
		}
	}
	return inner
}

func (t gainTheme) table(rows [][]string) string {
	return t.tableWith([]string{"#", "Program", "Count", "Saved", "Avg%", "Impact"},
		map[int]bool{0: true, 2: true, 3: true, 4: true}, rows)
}

// sourceTable renders the by-source breakdown (Source/Count/Saved/Avg%/Impact),
// right-aligning the numeric columns (count, saved, avg%).
func (t gainTheme) sourceTable(rows [][]string) string {
	return t.tableWith([]string{"Source", "Count", "Saved", "Avg%", "Impact"},
		map[int]bool{1: true, 2: true, 3: true}, rows)
}

// agentTable renders the per-agent breakdown (Agent/Commands/Saved/Avg%/Impact),
// right-aligning the numeric columns.
func (t gainTheme) agentTable(rows [][]string) string {
	return t.tableWith([]string{"Agent", "Commands", "Saved", "Avg%", "Impact"},
		map[int]bool{1: true, 2: true, 3: true}, rows)
}

func (t gainTheme) tableWith(headers []string, rightAlign map[int]bool, rows [][]string) string {
	headerStyle := t.Header
	cellStyle := t.Cell
	borderStyle := t.Border
	return table.New().
		Border(lipgloss.NormalBorder()).
		BorderLeft(false).
		BorderRight(false).
		BorderStyle(borderStyle).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := cellStyle
			if row == table.HeaderRow {
				style = headerStyle
			}
			if rightAlign[col] {
				return style.Align(lipgloss.Right).Padding(0, 1)
			}
			return style.Padding(0, 1)
		}).
		String()
}

func (t gainTheme) opportunityTable(rows [][]string) string {
	headerStyle := t.Header
	cellStyle := t.Cell
	borderStyle := t.Border
	return table.New().
		Border(lipgloss.NormalBorder()).
		BorderLeft(false).
		BorderRight(false).
		BorderStyle(borderStyle).
		Headers("#", "Program", "Mode", "Filter", "Count", "Emitted", "Saved%").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				if col == 0 || col == 4 || col == 5 || col == 6 {
					return headerStyle.Align(lipgloss.Right).Padding(0, 1)
				}
				return headerStyle.Padding(0, 1)
			}
			if col == 0 || col == 4 || col == 5 || col == 6 {
				return cellStyle.Align(lipgloss.Right).Padding(0, 1)
			}
			return cellStyle.Padding(0, 1)
		}).
		String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func approxTokens(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

func humanTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("~%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("~%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("~%.1fM", float64(n)/1_000_000)
}
