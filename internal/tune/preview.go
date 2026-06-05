package tune

import (
	"fmt"
	"strings"

	"ctx-wire/internal/explain"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/ui"
)

// PreviewSample is one sanitized sample command that a bundle would include.
// The Sample field is already run through the Sanitizer.
type PreviewSample struct {
	Program      string
	Mode         string
	Filter       string
	Class        string
	EmittedBytes int64
	SavedPct     float64
	Sample       string
}

// Preview is the dry-run view of what `ctx-wire tune bundle` would include.
// Building or formatting a Preview is pure: it writes no files, captures no
// output, and makes no network calls.
type Preview struct {
	HasData  bool
	Commands int
	Samples  []PreviewSample
	Omitted  int // samples beyond the --top cap, acknowledged not hidden
}

// manifestEntry describes one file a bundle contains. Preview writes none of
// them; the manifest is informational only.
type manifestEntry struct {
	name string
	desc string
}

var bundleManifest = []manifestEntry{
	{"summary.json", "aggregate counts, byte totals, and the time window"},
	{"report.txt", "the ctx-wire tune report"},
	{"suggestions.json", "per-class filter suggestions"},
	{"samples/commands.jsonl", "the sanitized sample commands shown below"},
	{"privacy_report.txt", "what was included/excluded and the sanitizer rules applied"},
}

// BuildPreview assembles the dry-run preview from a gain summary, sanitizing
// every sample command with san. opts.TopN caps the number of samples shown.
func BuildPreview(s *gain.Summary, san Sanitizer, opts Options) Preview {
	p := Preview{Commands: s.Commands, HasData: s.Commands > 0}
	for _, o := range s.Opportunities {
		p.Samples = append(p.Samples, PreviewSample{
			Program:      o.Program,
			Mode:         o.Mode,
			Filter:       o.Filter,
			Class:        classLabel(o),
			EmittedBytes: o.EmittedBytes,
			SavedPct:     o.SavedPct(),
			Sample:       san.Sample(o.Sample),
		})
	}
	if opts.TopN > 0 && len(p.Samples) > opts.TopN {
		p.Omitted = len(p.Samples) - opts.TopN
		p.Samples = p.Samples[:opts.TopN]
	}
	return p
}

// classLabel maps an opportunity to a short, tune-aligned class label.
func classLabel(o gain.OpportunityStat) string {
	if sec, ok := sectionFor(explain.Classify(o)); ok {
		return sectionTitle[sec]
	}
	return "Hook limitation"
}

// FormatPreview renders a Preview as plain terminal text.
func FormatPreview(p Preview) string {
	return FormatPreviewThemed(p, ui.Plain())
}

// FormatPreviewThemed renders a Preview with terminal styling. It explicitly
// states that nothing is written, that no raw output samples are included in
// preview mode, and the privacy guarantees that hold for a bundle.
func FormatPreviewThemed(p Preview, theme ui.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.Heading("ctx-wire tune preview: bundle dry run (nothing is written)"))
	if !p.HasData {
		b.WriteString("no commands recorded yet; run some commands through ctx-wire first\n")
		return b.String()
	}

	fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Bundle manifest (preview only, no files are written)"))
	for _, e := range bundleManifest {
		fmt.Fprintf(&b, "  - %-22s %s\n", theme.Command.Render(e.name), theme.Dim.Render(e.desc))
	}
	fmt.Fprintf(&b, "  %s\n", theme.Dim.Render("(these are produced by `ctx-wire tune bundle`; preview writes none of them)"))

	fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Sanitized sample commands"))
	if len(p.Samples) == 0 {
		fmt.Fprintf(&b, "  %s\n", theme.Dim.Render("no token opportunities recorded; nothing to sample"))
	}
	for _, s := range p.Samples {
		fmt.Fprintf(&b, "  - %-14s %s emitted %s saved %s\n",
			theme.Command.Render(s.Program),
			theme.Dim.Render("["+s.Class+"]"),
			theme.Number.Render(ui.HumanBytes(s.EmittedBytes)),
			theme.PercentBare(s.SavedPct, false))
		fmt.Fprintf(&b, "      %s %s\n", theme.Label.Render("sample:"), theme.Command.Render(s.Sample))
	}
	if p.Omitted > 0 {
		fmt.Fprintf(&b, "  %s\n", theme.Dim.Render(fmt.Sprintf("... %d more (raise --top to show)", p.Omitted)))
	}

	fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Output samples"))
	fmt.Fprintf(&b, "  %s\n", theme.Dim.Render("none: ctx-wire tune does not capture raw command output in this phase"))

	fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Privacy"))
	for _, note := range []string{
		"secrets are scrubbed before display (scrub.Scrub)",
		"the user home directory is replaced with $HOME",
		"the current project root is replaced with $PROJECT",
		"long absolute paths are compacted, keeping the trailing segments",
		"sample command length is capped",
		"no process environment variables are included",
		"no full raw logs are included",
		"no network calls are made",
	} {
		fmt.Fprintf(&b, "  %s %s\n", theme.OK.Render("-"), theme.Dim.Render(note))
	}
	return b.String()
}
