// Package explain provides diagnostics for ctx-wire: why a command would or
// would not be rewritten and filtered, and which recorded commands are still
// burning tokens. It is read-only and reuses the rewrite, filter, runner, and
// gain packages so its explanations can never drift from runtime behavior. It
// never mutates configuration.
package explain

import (
	"fmt"
	"strings"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/rewrite"
	"ctx-wire/internal/runner"
	"ctx-wire/internal/ui"
)

// Runner-decision modes reported for a wrapped command.
const (
	ModeFiltered = "filtered"
	ModeLive     = "live passthrough"
	ModeBypass   = "inherited bypass"
)

// SegmentReport is the full diagnostic for one command segment.
type SegmentReport struct {
	Command    string // the trimmed segment
	Wrapped    bool   // hook would wrap it in `ctx-wire run`
	HookReason string // when not wrapped, why (rewrite.Reason* text)
	Rewritten  string // the wrapped form (set when Wrapped)
	Inner      string // the command `ctx-wire run` receives (set when Wrapped)

	// Runner decision (only meaningful when Wrapped; otherwise the command never
	// reaches `ctx-wire run`).
	RunnerMode   string // ModeFiltered | ModeLive | ModeBypass
	Filter       string // matched filter name (ModeFiltered)
	Normalized   bool   // filter matched only after path-program normalization
	FilterReason string // why no filter matched (ModeLive)
	BypassReason string // why bypassed (ModeBypass)
}

// Report is the diagnostic for a whole command line.
type Report struct {
	Original string
	Segments []SegmentReport
}

// Command builds a diagnostic report for a command line without running it.
func Command(reg *filter.Registry, line string) Report {
	le := rewrite.Explain(line)
	rep := Report{Original: line}
	for _, s := range le.Segments {
		sr := SegmentReport{Command: s.Command, Wrapped: s.Wrapped, HookReason: s.Reason}
		if s.Wrapped {
			sr.Rewritten = s.Rewritten
			sr.Inner = s.Inner
			// s.Inner is the command ctx-wire run actually receives (for a `time`
			// prefix, the timed command), so classify the runner decision on it.
			name, args := splitArgs(s.Inner)
			if bypass, reason := runner.ClassifyBypass(name, args); bypass {
				sr.RunnerMode = ModeBypass
				sr.BypassReason = reason
			} else if d := reg.Explain(s.Inner); d.Matched {
				sr.RunnerMode = ModeFiltered
				sr.Filter = d.Name
				sr.Normalized = d.Normalized
			} else {
				sr.RunnerMode = ModeLive
				sr.FilterReason = "no built-in filter matches this command"
			}
		}
		rep.Segments = append(rep.Segments, sr)
	}
	return rep
}

// FormatCommand renders a Report as plain terminal text.
func FormatCommand(r Report) string {
	return FormatCommandThemed(r, ui.Plain())
}

// FormatCommandThemed renders a Report with terminal styling.
func FormatCommandThemed(r Report, theme ui.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", theme.Label.Render("command:"), theme.Command.Render(r.Original))
	multi := len(r.Segments) > 1
	for i, s := range r.Segments {
		indent := "  "
		if multi {
			fmt.Fprintf(&b, "  %s %s\n", theme.Label.Render(fmt.Sprintf("segment %d:", i+1)), theme.Command.Render(s.Command))
			indent = "    "
		}
		if s.Wrapped {
			fmt.Fprintf(&b, "%s%s %s -> %s\n", indent, theme.Label.Render("hook:  "), theme.OK.Render("wrapped"), theme.Command.Render(s.Rewritten))
			switch s.RunnerMode {
			case ModeFiltered:
				note := ""
				if s.Normalized {
					note = " (via path normalization)"
				}
				fmt.Fprintf(&b, "%s%s %s (filter: %s)%s\n", indent, theme.Label.Render("runner:"), theme.OK.Render("filtered"), theme.Number.Render(s.Filter), theme.Dim.Render(note))
			case ModeLive:
				fmt.Fprintf(&b, "%s%s %s (%s)\n", indent, theme.Label.Render("runner:"), theme.Warn.Render("live passthrough"), theme.Dim.Render(s.FilterReason))
			case ModeBypass:
				fmt.Fprintf(&b, "%s%s %s (%s)\n", indent, theme.Label.Render("runner:"), theme.Warn.Render("inherited bypass"), theme.Dim.Render(s.BypassReason))
			}
		} else {
			fmt.Fprintf(&b, "%s%s %s (%s)\n", indent, theme.Label.Render("hook:  "), theme.Warn.Render("passthrough"), theme.Dim.Render(s.HookReason))
			fmt.Fprintf(&b, "%s%s %s\n", indent, theme.Label.Render("runner:"), theme.Dim.Render("not invoked (command not routed through ctx-wire)"))
		}
	}
	return b.String()
}

// Class is a labeled diagnostic category for a token opportunity. Each
// opportunity maps to exactly one; the no-arg explain groups its output by
// class so real gaps stand out from expected limitations. It is exported so
// `ctx-wire tune` reuses the same classification instead of duplicating it.
type Class int

const (
	ClassMissingFilter Class = iota
	ClassCommonPassthrough
	ClassHookLimitation
	ClassFilteredWeak
	ClassPayloadConservative
)

// classMeta is the heading and default-action hint for a class. classOrder
// fixes the print order: actionable gaps first, expected limitations last.
type classMeta struct {
	title string
	hint  string
}

var (
	classOrder = []Class{
		ClassMissingFilter,
		ClassFilteredWeak,
		ClassCommonPassthrough,
		ClassPayloadConservative,
		ClassHookLimitation,
	}
	classInfo = map[Class]classMeta{
		ClassMissingFilter:       {"Missing filter (unsupported command)", "add a built-in filter for this command"},
		ClassFilteredWeak:        {"Filtered but weak", "review or tune the filter for high-volume cases"},
		ClassCommonPassthrough:   {"Common utility passthrough", "broaden the existing filter to match this invocation"},
		ClassPayloadConservative: {"Source/search payload (filtered conservatively)", "tune only if repeated or unexpectedly large"},
		ClassHookLimitation:      {"Hook limitation (pipeline/redirect/interactive)", "expected; ctx-wire cannot transparently wrap these"},
	}
)

// Classify assigns a token opportunity to exactly one diagnostic class. It is
// the single source of truth for opportunity classification, shared by the
// no-arg explain report and by `ctx-wire tune`.
func Classify(o gain.OpportunityStat) Class {
	decision := gain.OpportunityPolicyForStat(o, 0)
	switch decision.Kind {
	case gain.OpportunityHookLimited:
		return ClassHookLimitation
	case gain.OpportunityMissingFilter:
		if isCommonCommand(o.Program) {
			return ClassCommonPassthrough
		}
		return ClassMissingFilter
	case gain.OpportunityExpectedPayload:
		return ClassPayloadConservative
	case gain.OpportunityWeakFilter:
		return ClassFilteredWeak
	default:
		return ClassFilteredWeak
	}
}

// FormatOpportunities renders the token opportunities from a gain summary,
// grouped into labeled diagnostic classes so real gaps (missing/weak filters)
// stand out from expected limitations (hook/pipeline/payload). It is diagnostic
// only and never changes configuration. Commands excluded by the opportunity
// policy are acknowledged in a footer, never hidden.
func FormatOpportunities(s *gain.Summary) string {
	return FormatOpportunitiesThemed(s, ui.Plain())
}

// FormatOpportunitiesThemed renders grouped token opportunities with styling.
func FormatOpportunitiesThemed(s *gain.Summary, theme ui.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.Title.Render("ctx-wire explain: token opportunities"))
	fmt.Fprintf(&b, "%s\n", theme.Rule.Render(strings.Repeat("=", 62)))
	if s.Commands == 0 {
		b.WriteString("no commands recorded yet; run some commands through ctx-wire first\n")
		return b.String()
	}

	grouped := map[Class][]gain.OpportunityStat{}
	var oppExecutions int
	for _, o := range s.Opportunities {
		grouped[Classify(o)] = append(grouped[Classify(o)], o)
		oppExecutions += o.Count
	}

	if len(s.Opportunities) == 0 {
		b.WriteString("no token opportunities found: recorded commands are filtered well\n")
	}
	for _, cls := range classOrder {
		rows := grouped[cls]
		if len(rows) == 0 {
			continue
		}
		meta := classInfo[cls]
		fmt.Fprintf(&b, "\n%s\n", theme.Section.Render(meta.title))
		fmt.Fprintf(&b, "  %s %s\n", theme.Label.Render("hint:"), theme.Dim.Render(meta.hint))
		for _, o := range rows {
			fmt.Fprintf(&b, "  - %-14s emitted %-9s saved %s  (count %s)\n",
				theme.Command.Render(o.Program), theme.Number.Render(ui.HumanBytes(o.EmittedBytes)),
				theme.PercentBare(o.SavedPct(), false), theme.Number.Render(fmt.Sprintf("%d", o.Count)))
			if o.Sample != "" {
				fmt.Fprintf(&b, "      %s %s\n", theme.Label.Render("sample:"), theme.Command.Render(o.Sample))
			}
			if hint := rowHint(cls, o); hint != "" {
				fmt.Fprintf(&b, "      %s %s\n", theme.Label.Render("try:"), theme.Dim.Render(hint))
			}
		}
	}

	// Acknowledge commands that the central opportunity policy did not include
	// without listing them, so nothing is silently hidden.
	if lowVol := s.Commands - oppExecutions; lowVol > 0 {
		fmt.Fprintf(&b, "\n%s\n", theme.Section.Render("Not actionable"))
		fmt.Fprintf(&b, "  %s command(s) below threshold, already well-filtered, or expected payload\n", theme.Number.Render(fmt.Sprintf("%d", lowVol)))
	}
	return b.String()
}

// commonCommands are well-known utilities that ship a built-in filter. When one
// shows up as passthrough, the filter simply did not match that invocation, so
// the fix is to broaden an existing filter rather than write a brand-new one.
var commonCommands = map[string]bool{
	"cat": true, "sed": true, "head": true, "tail": true, "nl": true,
	"rg": true, "grep": true, "awk": true, "wc": true, "fd": true,
	"fdfind": true, "pwd": true, "which": true, "whereis": true,
	"ls": true, "find": true, "tree": true, "lsof": true, "env": true, "printenv": true,
	"strings": true, "agent-browser": true,
}

func isCommonCommand(prog string) bool { return commonCommands[prog] }

func rowHint(cls Class, o gain.OpportunityStat) string {
	if cls != ClassPayloadConservative {
		return ""
	}
	return gain.CommandShapeHint(o.Program, o.Sample)
}

// TeeLogHint delegates to gain's central opportunity policy helper.
func TeeLogHint(sample string) string { return gain.TeeLogHint(sample) }

// SearchLineNumberHint delegates to gain's central opportunity policy helper.
func SearchLineNumberHint(program, sample string) string {
	return gain.SearchLineNumberHint(program, sample)
}

// SampleInner strips a leading `ctx-wire run ` from a sample so the underlying
// command shape can be classified. Exported for reuse by `ctx-wire tune`; it
// delegates to gain.StripRunPrefix so the prefix rule lives in one place.
func SampleInner(sample string) string {
	return gain.StripRunPrefix(sample)
}

// splitArgs splits a command string into program + args, respecting single and
// double quotes. It is a diagnostic-grade tokenizer, not a full shell parser.
func splitArgs(s string) (name string, args []string) {
	var toks []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	if len(toks) == 0 {
		return "", nil
	}
	return toks[0], toks[1:]
}
