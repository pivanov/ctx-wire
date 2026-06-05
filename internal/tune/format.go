package tune

import (
	"fmt"
	"strings"

	"ctx-wire/internal/ui"
)

// Format renders a tune Report as plain terminal text.
func Format(r Report, opts Options) string {
	return FormatThemed(r, opts, ui.Plain())
}

// FormatThemed renders a tune Report with terminal styling. It is diagnostic
// only and never changes configuration. Actionable sections print first, then
// command-shape hints, then acknowledgment footers (hook limitations and
// non-actionable commands) so nothing recorded is silently hidden.
func FormatThemed(r Report, opts Options, theme ui.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.Heading("ctx-wire tune: filter improvement report"))
	if !r.HasData {
		b.WriteString("no commands recorded yet; run some commands through ctx-wire first\n")
		return b.String()
	}

	actionable := false
	for _, sec := range sectionOrder {
		rows := r.Sections[sec]
		if len(rows) == 0 {
			continue
		}
		actionable = true
		printSection(&b, theme, sectionTitle[sec], rows, opts.TopN)
	}
	if len(r.ShapeHints) > 0 {
		actionable = true
		printShapeHints(&b, theme, r.ShapeHints, opts.TopN)
	}
	if !actionable {
		b.WriteString("\nno filter gaps found: recorded commands are filtered well\n")
	}

	if r.HookLimited > 0 {
		fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Hook limitations (pipeline/redirect/interactive)"))
		fmt.Fprintf(&b, "  %s command(s) ctx-wire cannot transparently wrap (expected)\n",
			theme.Number.Render(fmt.Sprintf("%d", r.HookLimited)))
	}
	if r.NowCovered > 0 {
		fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Already covered"))
		fmt.Fprintf(&b, "  %s passthrough command(s) recorded before a filter or path-normalization fix; current filters match them now\n",
			theme.Number.Render(fmt.Sprintf("%d", r.NowCovered)))
	}
	if r.LowVolume > 0 {
		fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Not actionable"))
		fmt.Fprintf(&b, "  %s command(s) below threshold, already well-filtered, or expected payload\n",
			theme.Number.Render(fmt.Sprintf("%d", r.LowVolume)))
	}
	return b.String()
}

func printSection(b *strings.Builder, theme ui.Theme, title string, rows []Row, topN int) {
	fmt.Fprintf(b, "\n%s\n", theme.Section.Render(title))
	shown := rows
	if topN > 0 && len(rows) > topN {
		shown = rows[:topN]
	}
	for _, row := range shown {
		fmt.Fprintf(b, "  - %-14s emitted %-9s saved %s  (count %s)\n",
			theme.Command.Render(row.Program),
			theme.Number.Render(ui.HumanBytes(row.EmittedBytes)),
			theme.PercentBare(row.SavedPct, false),
			theme.Number.Render(fmt.Sprintf("%d", row.Count)))
		if row.Sample != "" {
			fmt.Fprintf(b, "      %s %s\n", theme.Label.Render("sample: "), theme.Command.Render(row.Sample))
		}
		if row.Suggestion != "" {
			fmt.Fprintf(b, "      %s %s\n", theme.Label.Render("suggest:"), theme.Dim.Render(row.Suggestion))
		}
	}
	printTruncation(b, theme, len(rows), topN)
}

func printShapeHints(b *strings.Builder, theme ui.Theme, hints []ShapeHint, topN int) {
	fmt.Fprintf(b, "\n%s\n", theme.Section.Render("Command-shape hints"))
	shown := hints
	if topN > 0 && len(hints) > topN {
		shown = hints[:topN]
	}
	for _, h := range shown {
		fmt.Fprintf(b, "  - %-14s %s\n", theme.Command.Render(h.Program), theme.Dim.Render(h.Hint))
		if h.Sample != "" {
			fmt.Fprintf(b, "      %s %s\n", theme.Label.Render("sample: "), theme.Command.Render(h.Sample))
		}
	}
	printTruncation(b, theme, len(hints), topN)
}

func printTruncation(b *strings.Builder, theme ui.Theme, total, topN int) {
	if topN > 0 && total > topN {
		fmt.Fprintf(b, "  %s\n", theme.Dim.Render(fmt.Sprintf("... %d more (raise --top to show)", total-topN)))
	}
}
