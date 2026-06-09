// Package ui centralizes terminal styling for human-facing ctx-wire commands.
// Protocol surfaces (hooks, MCP, and command output from `ctx-wire run`) should
// stay unstyled.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Mode selects the palette used when color is enabled.
type Mode int

const (
	ModeAuto Mode = iota
	ModeDark
	ModeLight
)

// Theme is the shared style set for terminal reports.
type Theme struct {
	Color    bool
	Dark     bool
	Renderer *lipgloss.Renderer

	Title   lipgloss.Style
	Section lipgloss.Style
	Rule    lipgloss.Style
	Dim     lipgloss.Style
	Label   lipgloss.Style
	Number  lipgloss.Style
	Command lipgloss.Style
	Path    lipgloss.Style

	OK     lipgloss.Style
	Warn   lipgloss.Style
	Fail   lipgloss.Style
	Good   lipgloss.Style
	Bad    lipgloss.Style
	Header lipgloss.Style
	Cell   lipgloss.Style
	Border lipgloss.Style
}

// ForFile returns a theme suited for f. It honors CTX_WIRE_COLOR, FORCE_COLOR,
// NO_COLOR, TERM=dumb, and CTX_WIRE_THEME=auto|dark|light|lite.
func ForFile(f *os.File) Theme {
	mode := ModeFromEnv()
	color := ColorEnabled(f)
	dark := true
	switch mode {
	case ModeDark:
		dark = true
	case ModeLight:
		dark = false
	default:
		dark = detectDarkBackground(f)
	}
	return New(color, dark, f)
}

// Plain returns an unstyled theme for tests and piped/plain formatters.
func Plain() Theme {
	return New(false, true, io.Discard)
}

// HumanBytes formats a byte count as a human-readable string (e.g. "1.5 KB").
// It is the single shared implementation used by gain, explain, tune, and
// doctor so the formatting cannot drift between reports.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// New returns a theme with explicit color/background settings.
func New(color, dark bool, w io.Writer) Theme {
	if w == nil {
		w = io.Discard
	}
	r := lipgloss.NewRenderer(w)
	if !color {
		r.SetColorProfile(termenv.Ascii)
		st := r.NewStyle()
		return Theme{
			Renderer: r,
			Title:    st,
			Section:  st,
			Rule:     st,
			Dim:      st,
			Label:    st,
			Number:   st,
			Command:  st,
			Path:     st,
			OK:       st,
			Warn:     st,
			Fail:     st,
			Good:     st,
			Bad:      st,
			Header:   st,
			Cell:     st,
			Border:   st,
		}
	}

	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(dark)
	p := paletteFor(dark)
	return Theme{
		Color:    true,
		Dark:     dark,
		Renderer: r,
		Title:    r.NewStyle().Bold(true).Foreground(lipgloss.Color(p.Green)),
		Section:  r.NewStyle().Bold(true).Foreground(lipgloss.Color(p.Green)),
		Rule:     r.NewStyle().Foreground(lipgloss.Color(p.Rule)),
		Dim:      r.NewStyle().Foreground(lipgloss.Color(p.Dim)),
		Label:    r.NewStyle().Foreground(lipgloss.Color(p.Label)),
		Number:   r.NewStyle().Foreground(lipgloss.Color(p.Cyan)),
		Command:  r.NewStyle().Foreground(lipgloss.Color(p.Cyan)),
		Path:     r.NewStyle().Foreground(lipgloss.Color(p.Blue)),
		OK:       r.NewStyle().Foreground(lipgloss.Color(p.Green)).Bold(true),
		Warn:     r.NewStyle().Foreground(lipgloss.Color(p.Yellow)).Bold(true),
		Fail:     r.NewStyle().Foreground(lipgloss.Color(p.Red)).Bold(true),
		Good:     r.NewStyle().Foreground(lipgloss.Color(p.Green)),
		Bad:      r.NewStyle().Foreground(lipgloss.Color(p.Red)),
		Header:   r.NewStyle().Bold(true).Foreground(lipgloss.Color(p.Header)),
		Cell:     r.NewStyle().Foreground(lipgloss.Color(p.Text)),
		Border:   r.NewStyle().Foreground(lipgloss.Color(p.Rule)),
	}
}

// ColorEnabled reports whether ctx-wire should emit ANSI color to f.
func ColorEnabled(f *os.File) bool {
	switch strings.ToLower(os.Getenv("CTX_WIRE_COLOR")) {
	case "always", "1", "true", "yes", "on":
		return true
	case "never", "0", "false", "no", "off":
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// ModeFromEnv parses CTX_WIRE_THEME. Unknown values fall back to auto.
func ModeFromEnv() Mode {
	switch strings.ToLower(os.Getenv("CTX_WIRE_THEME")) {
	case "dark":
		return ModeDark
	case "light", "lite":
		return ModeLight
	default:
		return ModeAuto
	}
}

func detectDarkBackground(f *os.File) bool {
	if f == nil {
		return true
	}
	out := termenv.NewOutput(f, termenv.WithColorCache(true))
	return out.HasDarkBackground()
}

// Status renders a fixed-width status marker like [ok  ].
// HeadingWidth is the shared rule width for command report headings, so every
// human-facing report lines up to the same column.
const HeadingWidth = 62

// fieldLabelWidth is the shared label column width for Field rows.
const fieldLabelWidth = 18

// Heading renders a report heading shared by all human-facing commands: the
// styled title on one line, then a rule beneath it. Using one helper keeps gain,
// telemetry, doctor, explain, and the trend views visually identical at the top.
func (t Theme) Heading(title string) string {
	return t.Title.Render(title) + "\n" + t.Rule.Render(strings.Repeat("─", HeadingWidth))
}

// Field renders an aligned "label  value" row: the label is themed and padded to
// a shared column so stacked key/value reports (gain totals, telemetry status,
// doctor) line up identically. A trailing colon is added if absent.
func (t Theme) Field(label, value string) string {
	if !strings.HasSuffix(label, ":") {
		label += ":"
	}
	return t.Label.Render(fmt.Sprintf("%-*s", fieldLabelWidth, label)) + " " + value
}

// Success renders the affirmative marker for command confirmations: a check on
// color terminals, "OK" in plain mode (stable for logs and tests). Shared so
// every "done" line (trust, telemetry, gain clear, init) looks the same.
func (t Theme) Success() string {
	if t.Color {
		return t.OK.Render("✓")
	}
	return t.OK.Render("OK")
}

func (t Theme) Status(status string) string {
	marker := fmt.Sprintf("[%-4s]", status)
	switch strings.ToLower(status) {
	case "ok":
		return t.OK.Render(marker)
	case "warn":
		return t.Warn.Render(marker)
	case "fail":
		return t.Fail.Render(marker)
	default:
		return t.Dim.Render(marker)
	}
}

func (t Theme) Percent(v float64) string {
	return t.PercentBare(v, true)
}

func (t Theme) PercentBare(v float64, parens bool) string {
	s := fmt.Sprintf("%.1f%%", v)
	if parens {
		s = "(" + s + ")"
	}
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

// Meter renders the filled/empty cells of a bar (no brackets) and is the single
// source of truth for bar fills across the CLI. Colored meters use green filled
// cells and gray empty cells; plain meters use # and . for stable logs/tests.
func (t Theme) Meter(filled, empty int) string {
	if filled < 0 {
		filled = 0
	}
	if empty < 0 {
		empty = 0
	}
	if !t.Color {
		return strings.Repeat("#", filled) + strings.Repeat(".", empty)
	}
	var s string
	if filled > 0 {
		s += t.Good.Render(strings.Repeat("░", filled))
	}
	if empty > 0 {
		s += t.Dim.Render(strings.Repeat("░", empty))
	}
	return s
}

// Bar renders a bracketed percent bar built from Meter cells.
func (t Theme) Bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return t.Meter(filled, width-filled)
}

type palette struct {
	Green  string
	Yellow string
	Red    string
	Cyan   string
	Blue   string
	Text   string
	Label  string
	Header string
	Dim    string
	Rule   string
}

func paletteFor(dark bool) palette {
	if dark {
		return palette{
			Green:  "#8eea7a",
			Yellow: "#f6e36f",
			Red:    "#ff8c8c",
			Cyan:   "#8bdbe6",
			Blue:   "#9cc6ff",
			Text:   "#d0d0d0",
			Label:  "#b8b8b8",
			Header: "#cfcfcf",
			Dim:    "#4f4f4f",
			Rule:   "#404040",
		}
	}
	return palette{
		Green:  "#1f7a1f",
		Yellow: "#9a6700",
		Red:    "#b42318",
		Cyan:   "#006d8f",
		Blue:   "#0969da",
		Text:   "#24292f",
		Label:  "#57606a",
		Header: "#24292f",
		Dim:    "#8c959f",
		Rule:   "#d0d7de",
	}
}
