// Package tune turns recorded gain data into a higher-level, local-only
// improvement report for ctx-wire's own filters.
//
// It is strictly read-only: it never runs commands, reads raw output, writes
// files, captures samples, or makes network calls. It builds on gain.Summary
// and reuses explain's opportunity classification (explain.Classify) so its
// diagnostics can never drift from `ctx-wire explain`. Command samples come from
// the gain log already scrubbed of secrets at record time, so tune neither sees
// nor stores raw argv or output.
//
// The current gain schema (ts, command, filter, mode, raw_bytes, emitted_bytes,
// saved_bytes, exit_code) carries everything the Phase 1 report needs: byte
// counts drive the volume/savings columns, mode+filter+command drive the
// classification, and the scrubbed command is the representative sample.
package tune

import (
	"strings"

	"ctx-wire/internal/explain"
	"ctx-wire/internal/filter"
	"ctx-wire/internal/gain"
)

// Section is a tune report grouping. The values double as the print order:
// actionable filter gaps first, expected-payload last.
type Section int

const (
	// SectionMissingFilter holds passthrough commands that should be filtered:
	// either no built-in filter exists, or an existing one did not match.
	SectionMissingFilter Section = iota
	// SectionWeakFilter holds filtered tooling commands that save little.
	SectionWeakFilter
	// SectionPayload holds source/search/diff/list output expected to stay large.
	SectionPayload
)

// sectionOrder fixes the print order of the actionable sections.
var sectionOrder = []Section{SectionMissingFilter, SectionWeakFilter, SectionPayload}

var sectionTitle = map[Section]string{
	SectionMissingFilter: "Missing filters",
	SectionWeakFilter:    "Weak filters",
	SectionPayload:       "Payload commands (expected to stay large)",
}

// Row is one program/filter opportunity within an actionable section.
type Row struct {
	Program      string
	Count        int
	EmittedBytes int64
	SavedPct     float64
	Sample       string
	Suggestion   string
}

// ShapeHint is a cross-cutting command-shape improvement (independent of which
// section a command lands in): e.g. a search without -n, an unscoped find, or a
// command that repeats absolute paths.
type ShapeHint struct {
	Program string
	Sample  string
	Hint    string
}

// Report is the analyzed, render-ready tune result. It always carries the full
// data set; row limiting (--top) is applied at format time so the report stays
// complete and testable.
type Report struct {
	Commands    int               // total recorded commands in the window
	HasData     bool              // false only when no commands were recorded
	Sections    map[Section][]Row // actionable rows, keyed by section
	ShapeHints  []ShapeHint       // command-shape hints, deduped by program+hint
	HookLimited int               // executions that ctx-wire cannot wrap (pipeline/redirect/interactive)
	LowVolume   int               // executions not included by the opportunity policy
	NowCovered  int               // passthrough executions the current filters already match (stale entries)
}

// Options controls how a Report is rendered.
type Options struct {
	// TopN caps the rows shown per section (and the shape-hint list). 0 means no
	// cap: show everything.
	TopN int
}

// Analyze builds a tune Report from a gain summary. The summary is expected to
// already be windowed (e.g. via gain.SummarizeWithOptions for --since); Analyze
// itself does no time filtering and never mutates the summary.
// Analyze builds the report from recorded gain data. When reg is non-nil, a
// command recorded as passthrough that the CURRENT filters would match is
// treated as already covered (a stale entry from before a filter or
// path-normalization fix) and acknowledged in a footer rather than flagged as a
// missing filter. Pass nil to classify purely on the recorded mode.
func Analyze(reg *filter.Registry, s *gain.Summary) Report {
	r := Report{
		Commands: s.Commands,
		HasData:  s.Commands > 0,
		Sections: map[Section][]Row{},
	}
	var oppExecutions int
	seenShape := map[string]bool{}
	for _, o := range s.Opportunities {
		oppExecutions += o.Count

		// Command-shape hints are cross-cutting: a command may also appear in one
		// of the actionable sections below. Dedupe by program+hint so repeated
		// invocations collapse to a single line.
		for _, sh := range shapeHints(o) {
			key := sh.Program + "\x00" + sh.Hint
			if seenShape[key] {
				continue
			}
			seenShape[key] = true
			r.ShapeHints = append(r.ShapeHints, sh)
		}

		cls := explain.Classify(o)
		sec, actionable := sectionFor(cls)
		if !actionable {
			// Pipeline/redirect/interactive passthrough: ctx-wire cannot wrap it,
			// so it is acknowledged in a footer rather than flagged as a gap.
			r.HookLimited += o.Count
			continue
		}
		// Stale-data guard: a passthrough opportunity that the current filters
		// already match was recorded before that filter (or path normalization)
		// existed. It is not a missing filter today, so acknowledge it instead.
		if sec == SectionMissingFilter && reg != nil && reg.Find(gain.StripRunPrefix(o.Sample)) != nil {
			r.NowCovered += o.Count
			continue
		}
		r.Sections[sec] = append(r.Sections[sec], Row{
			Program:      o.Program,
			Count:        o.Count,
			EmittedBytes: o.EmittedBytes,
			SavedPct:     o.SavedPct(),
			Sample:       o.Sample,
			Suggestion:   suggestionFor(cls),
		})
	}
	// Commands excluded by the central opportunity policy are acknowledged, never
	// hidden. This includes low-volume rows, well-filtered rows, and expected
	// payload that is not actionable.
	r.LowVolume = s.Commands - oppExecutions
	return r
}

// sectionFor maps an explain class to a tune section. The boolean is false for
// classes that are not actionable filter gaps (hook limitations).
func sectionFor(c explain.Class) (Section, bool) {
	switch c {
	case explain.ClassMissingFilter, explain.ClassCommonPassthrough:
		return SectionMissingFilter, true
	case explain.ClassFilteredWeak:
		return SectionWeakFilter, true
	case explain.ClassPayloadConservative:
		return SectionPayload, true
	default: // explain.ClassHookLimitation
		return 0, false
	}
}

// suggestionFor returns the higher-level remediation hint for a class.
func suggestionFor(c explain.Class) string {
	switch c {
	case explain.ClassMissingFilter:
		return "add a built-in filter for this command"
	case explain.ClassCommonPassthrough:
		return "broaden the existing built-in filter to match this invocation"
	case explain.ClassFilteredWeak:
		return "review or tune the filter; it saves little on these runs"
	case explain.ClassPayloadConservative:
		return "expected payload (filtered conservatively); tune only if repeated or unexpectedly large"
	default:
		return ""
	}
}

// shapeHints returns the command-shape improvements detectable from a single
// opportunity's sample. It reuses explain's search rule and adds tune-specific
// find/path checks.
func shapeHints(o gain.OpportunityStat) []ShapeHint {
	var hints []ShapeHint
	add := func(hint string) {
		if hint != "" {
			hints = append(hints, ShapeHint{Program: o.Program, Sample: o.Sample, Hint: hint})
		}
	}
	add(gain.CommandShapeHint(o.Program, o.Sample))
	add(broadFindHint(o))
	add(repeatedAbsPathHint(o))
	return hints
}

// broadFindHint flags a find invocation with no scoping predicate, which tends
// to walk far more of the tree than the agent needs.
func broadFindHint(o gain.OpportunityStat) string {
	if o.Program != "find" || o.Sample == "" {
		return ""
	}
	for _, tok := range strings.Fields(explain.SampleInner(o.Sample)) {
		switch tok {
		case "-maxdepth", "-name", "-iname", "-path", "-ipath", "-type", "-prune", "-regex":
			return ""
		}
	}
	return "scope find with -maxdepth/-name/-type so it walks less of the tree"
}

// repeatedAbsPathHint flags a sample that passes two or more absolute paths
// under a common directory, which is usually shorter as relative paths.
func repeatedAbsPathHint(o gain.OpportunityStat) string {
	if o.Sample == "" {
		return ""
	}
	byDir := map[string]int{}
	for _, tok := range strings.Fields(explain.SampleInner(o.Sample)) {
		if !strings.HasPrefix(tok, "/") || len(tok) < 2 {
			continue
		}
		byDir[parentDir(tok)]++
	}
	for _, n := range byDir {
		if n >= 2 {
			return "command repeats absolute paths under one directory; cd there and use relative paths"
		}
	}
	return ""
}

// parentDir returns the directory portion of a unix-style path token. Samples
// are stored unix-style, so a plain '/' split is correct and OS-independent.
func parentDir(path string) string {
	if i := strings.LastIndex(path, "/"); i > 0 {
		return path[:i]
	}
	return "/"
}
