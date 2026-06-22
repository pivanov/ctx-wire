package discover

import (
	"fmt"
	"strings"
	"time"

	"ctx-wire/internal/ui"
)

var catLabel = map[Category]string{
	CatEscaped:        "Escaped (ctx-wire never filtered these)",
	CatCovered:        "Covered (possibly filtered by ctx-wire)",
	CatPassthrough:    "Passthrough by design (pipeline/redirect/builtin)",
	CatHookLimited:    "Hook-limited (interactive/streaming)",
	CatPredatesLedger: "Predates gain ledger (can't correlate)",
	CatUnknown:        "Unknown",
}

// Format renders a discover Report as plain terminal text.
func Format(r *Report, opts Options) string {
	return FormatThemed(r, opts, ui.Plain())
}

// FormatThemed renders a discover Report with terminal styling. It is read-only
// and local-only: it reports what was scanned and which commands escaped
// ctx-wire, ranked. Escaped commands are the actionable blind spot.
func FormatThemed(r *Report, opts Options, theme ui.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", theme.Heading("ctx-wire discover: escaped commands"))
	fmt.Fprintf(&b, "%s\n", theme.Dim.Render("agent commands ctx-wire never filtered"))

	scope := "all projects"
	if opts.Project != "" {
		scope = "this project"
	}
	fmt.Fprintf(&b, "%s %s  %s\n", theme.Label.Render("scope:"), theme.Dim.Render(scope),
		theme.Dim.Render(fmt.Sprintf("(claude: %d files, codex: %d files)", r.ClaudeFiles, r.CodexFiles)))

	if !r.LedgerStart.IsZero() {
		fmt.Fprintf(&b, "%s %s\n", theme.Label.Render("gain ledger:"),
			theme.Dim.Render(fmt.Sprintf("%s .. %s  (commands before the start can't be correlated)",
				r.LedgerStart.Format(time.RFC3339), r.LedgerEnd.Format(time.RFC3339))))
	}

	if r.Total == 0 {
		b.WriteString("\nno agent commands found; checked Claude and Codex transcripts (read-only)\n")
		if len(r.Scanned) == 0 {
			b.WriteString("no Claude or Codex transcripts were found in the expected locations\n")
		}
		return b.String()
	}

	fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Summary"))
	for _, c := range catOrder {
		n := r.ByCategory[c]
		if n == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %-50s %s\n", catLabel[c], theme.Number.Render(fmt.Sprintf("%d", n)))
	}
	fmt.Fprintf(&b, "  %-50s %s\n", "Total commands seen", theme.Number.Render(fmt.Sprintf("%d", r.Total)))

	if len(r.Escaped) > 0 {
		fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Escaped commands (point ctx-wire here next)"))
		fmt.Fprintf(&b, "  %s\n", theme.Dim.Render("ctx-wire would filter these, but no gain record matched: hook not firing, run raw, or via another tool"))
		shown := r.Escaped
		if opts.TopN > 0 && len(shown) > opts.TopN {
			shown = shown[:opts.TopN]
		}
		for _, row := range shown {
			fmt.Fprintf(&b, "  - %s %s  %s\n",
				theme.Number.Render(fmt.Sprintf("x%d", row.Count)),
				theme.Command.Render(row.Command),
				theme.Dim.Render("["+strings.Join(row.Agents, ",")+"]"))
		}
		if opts.TopN > 0 && len(r.Escaped) > opts.TopN {
			fmt.Fprintf(&b, "  %s\n", theme.Dim.Render(fmt.Sprintf("... %d more (raise --top to show)", len(r.Escaped)-opts.TopN)))
		}
	} else {
		fmt.Fprintf(&b, "\n%s\n", theme.Dim.Render("no escaped commands: everything ctx-wire could filter went through it"))
	}

	fmt.Fprintf(&b, "\n%s %s\n", theme.Label.Render("note:"),
		theme.Dim.Render("read-only, no network; matching is conservative (captured = possibly, escaped = definitely not in gain)"))
	return b.String()
}
