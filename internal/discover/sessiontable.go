package discover

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"ctx-wire/internal/ui"
)

// sessionTable renders the per-session adoption breakdown as a box-drawing table
// in the same style as `ctx-wire gain` (lipgloss NormalBorder, no left/right
// edge, dim borders), so the two reports read as one product. The Adoption cell
// is the colored bar plus percentage and stays the last column, where its ANSI
// codes cannot disturb the alignment of the plain columns before it.
func sessionTable(stats []SessionStat, theme ui.Theme) string {
	rows := make([][]string, 0, len(stats))
	for _, s := range stats {
		file := s.File
		if len(file) > 30 {
			file = file[:29] + "…"
		}
		const width = 10
		filled := int(float64(width) * s.AdoptionPct() / 100)
		if filled < 0 {
			filled = 0
		}
		if filled > width {
			filled = width
		}
		bar := theme.Meter(filled, width-filled)
		adoption := bar + " " + theme.Number.Render(fmt.Sprintf("%.1f%%", s.AdoptionPct()))
		rows = append(rows, []string{
			s.Agent, file,
			fmt.Sprintf("%d", s.Coverable),
			fmt.Sprintf("%d", s.Covered),
			adoption,
		})
	}
	rightAlign := map[int]bool{2: true, 3: true}
	return table.New().
		Border(lipgloss.NormalBorder()).
		BorderLeft(false).
		BorderRight(false).
		BorderStyle(theme.Border).
		Headers("Agent", "Session", "Cmds", "Used", "Adoption").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := theme.Cell
			if row == table.HeaderRow {
				style = theme.Header
			}
			if rightAlign[col] {
				return style.Align(lipgloss.Right).Padding(0, 1)
			}
			return style.Padding(0, 1)
		}).
		String()
}
