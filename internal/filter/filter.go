// Package filter implements ctx-wire's declarative TOML filter pipeline. It
// compresses command output through an 8-stage pipeline driven by TOML filter
// definitions.
//
// Pipeline stages (applied in order):
//  1. strip_ansi        remove ANSI escape codes
//  2. replace           regex substitutions, line-by-line, chainable
//  3. match_output      short-circuit: if blob matches, return message (unless guard)
//  4. strip/keep_lines  filter lines by regex
//  5. truncate_lines_at truncate each line to N chars
//  6. head/tail_lines   keep first/last N lines
//  7. max_lines         absolute line cap
//  8. on_empty          message if result is empty
package filter

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiRE matches ANSI CSI escape sequences, including DEC private modes such as
// \x1b[?25l and alternate-screen toggles.
var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// oscRE matches Operating System Command escape sequences, including terminal
// hyperlinks and title changes terminated by BEL or ST.
var oscRE = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// ---------------------------------------------------------------------------
// TOML schema (deserialization)
// ---------------------------------------------------------------------------

type tomlFile struct {
	SchemaVersion int                   `toml:"schema_version"`
	Filters       map[string]tomlFilter `toml:"filters"`
	Tests         map[string][]tomlTest `toml:"tests"`
}

type tomlFilter struct {
	Description        string            `toml:"description"`
	MatchCommand       string            `toml:"match_command"`
	Priority           int               `toml:"priority"`
	StripANSI          bool              `toml:"strip_ansi"`
	Replace            []tomlReplace     `toml:"replace"`
	MatchOutput        []tomlMatchOutput `toml:"match_output"`
	StripLinesMatching []string          `toml:"strip_lines_matching"`
	KeepLinesMatching  []string          `toml:"keep_lines_matching"`
	TruncateLinesAt    *int              `toml:"truncate_lines_at"`
	HeadLines          *int              `toml:"head_lines"`
	TailLines          *int              `toml:"tail_lines"`
	MaxLines           *int              `toml:"max_lines"`
	OnEmpty            *string           `toml:"on_empty"`
	FilterStderr       bool              `toml:"filter_stderr"`
	GroupBy            *tomlGroupBy      `toml:"group_by"`
	// ReduceJSON opts a filter into reducing JSON output. By default the runner
	// passes a complete, valid JSON document through untouched (the documented
	// "JSON payloads are not reduced" guarantee, applied by content). A filter
	// like jq, whose whole purpose is to compact large JSON, sets this true to
	// keep capping/truncating its JSON output.
	ReduceJSON bool `toml:"reduce_json"`
}

// tomlGroupBy groups grep/rg-style `key:rest` output: it keeps the first
// max_per_group lines per key, caps the number of keys at max_groups, and
// summarizes what was omitted. Lines that do not match key pass through.
type tomlGroupBy struct {
	Key         string `toml:"key"`           // regex; capture group 1 is the group key
	MaxPerGroup int    `toml:"max_per_group"` // lines kept per group (>0)
	MaxGroups   int    `toml:"max_groups"`    // groups kept total (>0)
	OmitLabel   string `toml:"omit_label"`    // fmt template, args: (omittedCount int, groupKey string)
}

type tomlReplace struct {
	Pattern     string `toml:"pattern"`
	Replacement string `toml:"replacement"`
}

type tomlMatchOutput struct {
	Pattern string  `toml:"pattern"`
	Message string  `toml:"message"`
	Unless  *string `toml:"unless"`
}

type tomlTest struct {
	Name     string `toml:"name"`
	Input    string `toml:"input"`
	Expected string `toml:"expected"`
	// Draft marks a test scaffolded by `tune draft` whose expected output still
	// mirrors the filter (so it asserts nothing yet). verify flags it until the
	// author trims expected and removes the marker; a built-in must never ship
	// with it set.
	Draft bool `toml:"draft"`
	// Failed runs the case as the runner does for a non-zero exit code: it
	// suppresses synthetic-success messages (match_output / on_empty) and keeps
	// the tail on truncation. Use it to assert a filter never hides a failure
	// (e.g. a location-less `fatal error: ...`) behind a "no issues" summary.
	Failed bool `toml:"failed"`
	// Stdout/Stderr model a real command's TWO streams, so a filter that targets
	// the wrong one is caught. The observed output is built the way the runner
	// presents it: with filter_stderr the filter sees stdout+stderr; without it the
	// filter sees stdout only and raw stderr follows. When either is set, Input is
	// ignored. This is the fixture that exposes the "diagnostics on stderr but no
	// filter_stderr" class (a biome test written this way goes red without it).
	Stdout string `toml:"stdout"`
	Stderr string `toml:"stderr"`
	// MinSavedPercent asserts the observed output is at least this much smaller (by
	// bytes) than the combined raw input. It is the regression guard a single
	// exact-match cannot be: a filter running on the wrong stream saves ~0% and
	// fails it. Combine with stdout/stderr; pairs with or replaces Expected.
	MinSavedPercent int `toml:"min_saved_percent"`
}

// ---------------------------------------------------------------------------
// Compiled types
// ---------------------------------------------------------------------------

type compiledReplace struct {
	pattern     *regexp.Regexp
	replacement string
}

type compiledMatchOutput struct {
	pattern *regexp.Regexp
	message string
	unless  *regexp.Regexp
}

type lineFilterKind int

const (
	lineFilterNone lineFilterKind = iota
	lineFilterStrip
	lineFilterKeep
)

// CompiledFilter is a parsed, regex-compiled filter ready to apply.
type CompiledFilter struct {
	Name         string
	Description  string
	Priority     int
	FilterStderr bool

	matchRegex      *regexp.Regexp
	stripANSI       bool
	replace         []compiledReplace
	matchOutput     []compiledMatchOutput
	lineFilterKind  lineFilterKind
	lineFilterSet   []*regexp.Regexp
	truncateLinesAt *int
	headLines       *int
	tailLines       *int
	maxLines        *int
	onEmpty         *string
	groupBy         *compiledGroupBy
	reduceJSON      bool
}

type compiledGroupBy struct {
	key         *regexp.Regexp
	maxPerGroup int
	maxGroups   int
	omitLabel   string
}

// MaxLines exposes the absolute line cap (nil if unset).
func (f *CompiledFilter) MaxLines() *int { return f.maxLines }

// ReducesJSON reports whether this filter opts into reducing JSON output (so the
// runner's content-based JSON passthrough must not override it).
func (f *CompiledFilter) ReducesJSON() bool { return f.reduceJSON }

// ApplyResult is the output of a filter plus metadata about whether the filter
// dropped content due to an explicit cap.
type ApplyResult struct {
	Output    string
	Truncated bool
}

// ApplyOptions controls context-sensitive parts of the pipeline. The default is
// the normal behavior used by verify and successful command output.
type ApplyOptions struct {
	// SuppressSyntheticSuccess disables match_output short-circuit messages and
	// on_empty messages. Runners use this for non-zero exit codes so a failed
	// command can still be cleaned/capped, but ctx-wire never invents an "ok"
	// message for it.
	SuppressSyntheticSuccess bool

	// TruncateLevel scales the filter's numeric caps for this invocation. The
	// zero value (LevelDefault) applies the TOML values as written, which keeps
	// verify and the conformance corpus deterministic; runners resolve the
	// user's level via ResolveTruncateLevel.
	TruncateLevel TruncateLevel

	// KeepTailOnTruncate makes the absolute max_lines cap keep the LAST lines
	// instead of the first. Runners set it for non-zero exit codes: when a
	// failed command's output overflows the cap, the actionable signal (the
	// test summary, the failing assertion, the final stack frame) is almost
	// always at the end, so the head-keep default would truncate exactly what
	// the agent needs.
	KeepTailOnTruncate bool
}

// compile turns a TOML filter definition into a CompiledFilter, compiling all
// runnerToken expands, inside a filter's match_command, to runnerPrefix: the
// canonical set of package-runner prefixes a CLI tool can be launched through
// (npx, bunx, pnpm|yarn dlx|exec, bun x). Defining it once here is what keeps
// every runner-able filter consistent: a filter writes "^(?:{{runner}})?tool\b"
// (or folds it into a larger alternation) and inherits the full set, instead of
// hand-rolling a partial prefix that silently drifts. The token is the bare
// alternation (no group, no trailing ?), so callers wrap it however they need.
// Coverage across filters is pinned by TestRunnerPrefixConsistency.
const (
	runnerToken  = "{{runner}}"
	runnerPrefix = `(?:npx|bunx)\s+|(?:pnpm|yarn)\s+(?:dlx|exec)\s+|bun\s+x\s+`
)

func expandRunnerToken(pattern string) string {
	return strings.ReplaceAll(pattern, runnerToken, runnerPrefix)
}

// regexes up front. Returns an error if any regex is invalid or if strip and
// keep are both set (mutually exclusive).
func compile(name string, def tomlFilter) (*CompiledFilter, error) {
	if len(def.StripLinesMatching) > 0 && len(def.KeepLinesMatching) > 0 {
		return nil, fmt.Errorf("strip_lines_matching and keep_lines_matching are mutually exclusive")
	}

	matchRegex, err := regexp.Compile(expandRunnerToken(def.MatchCommand))
	if err != nil {
		return nil, fmt.Errorf("invalid match_command regex: %w", err)
	}

	replace := make([]compiledReplace, 0, len(def.Replace))
	for _, r := range def.Replace {
		p, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid replace pattern %q: %w", r.Pattern, err)
		}
		replace = append(replace, compiledReplace{pattern: p, replacement: r.Replacement})
	}

	matchOutput := make([]compiledMatchOutput, 0, len(def.MatchOutput))
	for _, m := range def.MatchOutput {
		p, err := regexp.Compile(m.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid match_output pattern %q: %w", m.Pattern, err)
		}
		var unless *regexp.Regexp
		if m.Unless != nil {
			u, err := regexp.Compile(*m.Unless)
			if err != nil {
				return nil, fmt.Errorf("invalid match_output unless pattern %q: %w", *m.Unless, err)
			}
			unless = u
		}
		matchOutput = append(matchOutput, compiledMatchOutput{pattern: p, message: m.Message, unless: unless})
	}

	kind := lineFilterNone
	var set []*regexp.Regexp
	switch {
	case len(def.StripLinesMatching) > 0:
		kind = lineFilterStrip
		set, err = compileSet(def.StripLinesMatching)
		if err != nil {
			return nil, fmt.Errorf("invalid strip_lines_matching regex: %w", err)
		}
	case len(def.KeepLinesMatching) > 0:
		kind = lineFilterKeep
		set, err = compileSet(def.KeepLinesMatching)
		if err != nil {
			return nil, fmt.Errorf("invalid keep_lines_matching regex: %w", err)
		}
	}

	groupBy, err := compileGroupBy(def.GroupBy)
	if err != nil {
		return nil, err
	}

	return &CompiledFilter{
		Name:            name,
		Description:     def.Description,
		Priority:        def.Priority,
		FilterStderr:    def.FilterStderr,
		matchRegex:      matchRegex,
		stripANSI:       def.StripANSI,
		replace:         replace,
		matchOutput:     matchOutput,
		lineFilterKind:  kind,
		lineFilterSet:   set,
		truncateLinesAt: def.TruncateLinesAt,
		headLines:       def.HeadLines,
		tailLines:       def.TailLines,
		maxLines:        def.MaxLines,
		onEmpty:         def.OnEmpty,
		groupBy:         groupBy,
		reduceJSON:      def.ReduceJSON,
	}, nil
}

// compileGroupBy validates and compiles a group_by definition. A nil def yields
// a nil stage (no grouping). Invalid config returns a clear error so a bad
// filter fails load/verify instead of silently misbehaving.
func compileGroupBy(def *tomlGroupBy) (*compiledGroupBy, error) {
	if def == nil {
		return nil, nil
	}
	if def.Key == "" {
		return nil, fmt.Errorf("group_by.key is required")
	}
	key, err := regexp.Compile(def.Key)
	if err != nil {
		return nil, fmt.Errorf("invalid group_by.key regex: %w", err)
	}
	if key.NumSubexp() < 1 {
		return nil, fmt.Errorf("group_by.key %q must have a capture group for the group key", def.Key)
	}
	if def.MaxPerGroup < 1 {
		return nil, fmt.Errorf("group_by.max_per_group must be >= 1 (got %d)", def.MaxPerGroup)
	}
	if def.MaxGroups < 1 {
		return nil, fmt.Errorf("group_by.max_groups must be >= 1 (got %d)", def.MaxGroups)
	}
	if def.OmitLabel == "" {
		return nil, fmt.Errorf("group_by.omit_label is required")
	}
	// The label is formatted with (omittedCount int, groupKey string); reject a
	// template whose verbs do not match those arguments.
	if probe := fmt.Sprintf(def.OmitLabel, 1, "x"); strings.Contains(probe, "%!") {
		return nil, fmt.Errorf("group_by.omit_label %q must accept (int, string) format args", def.OmitLabel)
	}
	return &compiledGroupBy{
		key:         key,
		maxPerGroup: def.MaxPerGroup,
		maxGroups:   def.MaxGroups,
		omitLabel:   def.OmitLabel,
	}, nil
}

func compileSet(pats []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

func anyMatch(set []*regexp.Regexp, s string) bool {
	for _, re := range set {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Pipeline
// ---------------------------------------------------------------------------

// Apply runs the compiled filter pipeline over raw stdout. Pure string->string.
func Apply(f *CompiledFilter, stdout string) string {
	return ApplyWithMeta(f, stdout).Output
}

// ApplyWithMeta runs the compiled filter pipeline and reports whether a cap
// omitted content. Callers can use Truncated to keep the full scrubbed spool.
func ApplyWithMeta(f *CompiledFilter, stdout string) ApplyResult {
	return ApplyWithMetaOptions(f, stdout, ApplyOptions{})
}

// ApplyWithMetaOptions is ApplyWithMeta plus context-sensitive runner options.
func ApplyWithMetaOptions(f *CompiledFilter, stdout string, opts ApplyOptions) ApplyResult {
	truncated := false
	lines := splitLines(stdout)

	// 1. strip_ansi
	if f.stripANSI {
		for i, l := range lines {
			lines[i] = stripANSI(l)
		}
	}

	// 2. replace (line-by-line, rules chained sequentially)
	for _, rule := range f.replace {
		for i, l := range lines {
			lines[i] = rule.pattern.ReplaceAllString(l, rule.replacement)
		}
	}

	// 3. match_output (short-circuit on full blob, first rule wins)
	if len(f.matchOutput) > 0 && !opts.SuppressSyntheticSuccess {
		blob := strings.Join(lines, "\n")
		for _, rule := range f.matchOutput {
			if rule.pattern.MatchString(blob) {
				if rule.unless != nil && rule.unless.MatchString(blob) {
					continue
				}
				return ApplyResult{Output: rule.message, Truncated: truncated}
			}
		}
	}

	// 4. strip OR keep (mutually exclusive)
	switch f.lineFilterKind {
	case lineFilterStrip:
		lines = retain(lines, func(l string) bool { return !anyMatch(f.lineFilterSet, l) })
	case lineFilterKeep:
		lines = retain(lines, func(l string) bool { return anyMatch(f.lineFilterSet, l) })
	}

	// 5. truncate_lines_at
	if at := scaledCap(f.truncateLinesAt, opts.TruncateLevel); at != nil {
		for i, l := range lines {
			if utf8.RuneCountInString(l) > *at {
				truncated = true
			}
			lines[i] = truncate(l, *at)
		}
	}

	// 6. head + tail
	headCap := scaledCap(f.headLines, opts.TruncateLevel)
	tailCap := scaledCap(f.tailLines, opts.TruncateLevel)
	total := len(lines)
	switch {
	case headCap != nil && tailCap != nil:
		head, tail := *headCap, *tailCap
		if total > head+tail {
			truncated = true
			result := make([]string, 0, head+tail+1)
			result = append(result, lines[:head]...)
			result = append(result, fmt.Sprintf("... (%d lines omitted)", total-head-tail))
			result = append(result, lines[total-tail:]...)
			lines = result
		}
	case headCap != nil:
		head := *headCap
		if total > head {
			truncated = true
			lines = append(lines[:head:head], fmt.Sprintf("... (%d lines omitted)", total-head))
		}
	case tailCap != nil:
		tail := *tailCap
		if total > tail {
			truncated = true
			omitted := total - tail
			result := make([]string, 0, tail+1)
			result = append(result, fmt.Sprintf("... (%d lines omitted)", omitted))
			result = append(result, lines[omitted:]...)
			lines = result
		}
	}

	// 6b. group_by (group key:rest output; cap per group and total groups). Runs
	// after line cleanup/truncation and before the final absolute max_lines cap.
	if f.groupBy != nil {
		grouped, gtrunc := applyGroupBy(f.groupBy, lines,
			scaledCount(f.groupBy.maxPerGroup, opts.TruncateLevel),
			scaledCount(f.groupBy.maxGroups, opts.TruncateLevel))
		if gtrunc {
			truncated = true
		}
		lines = grouped
	}

	// 7. max_lines (absolute cap, counts omit messages)
	if m := scaledCap(f.maxLines, opts.TruncateLevel); m != nil {
		max := *m
		if len(lines) > max {
			truncated = true
			omitted := len(lines) - max
			msg := fmt.Sprintf("... (%d lines truncated)", omitted)
			if opts.KeepTailOnTruncate {
				// Keep the tail: on failure the error is at the end, not the top.
				lines = append([]string{msg}, lines[omitted:]...)
			} else {
				lines = append(lines[:max:max], msg)
			}
		}
	}

	// 8. on_empty
	result := strings.Join(lines, "\n")
	if ultraCompact {
		result = squeezeBlankLines(result)
	}
	if strings.TrimSpace(result) == "" && f.onEmpty != nil && !opts.SuppressSyntheticSuccess {
		return ApplyResult{Output: *f.onEmpty, Truncated: truncated}
	}
	return ApplyResult{Output: result, Truncated: truncated}
}

// ultraCompact, when set, applies an extra compaction pass to filtered output.
// Set once at startup from the user config ([output] ultra_compact); read-only
// after.
var ultraCompact bool

// SetUltraCompact enables or disables the extra compaction pass.
func SetUltraCompact(v bool) { ultraCompact = v }

// squeezeBlankLines trims trailing whitespace from every line and collapses any
// run of blank lines to a single blank, squeezing a few more tokens out of
// already-filtered output.
func squeezeBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			if blank > 0 {
				continue
			}
			blank++
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func stripANSI(s string) string {
	s = oscRE.ReplaceAllString(s, "")
	return ansiRE.ReplaceAllString(s, "")
}

// applyGroupBy groups lines whose group key (capture group 1 of g.key) repeats,
// keeping the first maxPerGroup lines per group and the first maxGroups groups,
// in original order. Lines that do not match g.key pass through untouched. It
// returns the new lines and whether anything was omitted. Output is identical to
// input when nothing exceeds the caps.
func applyGroupBy(g *compiledGroupBy, lines []string, perGroup, maxGroups int) ([]string, bool) {
	// Pass 1: establish group order and per-group totals.
	total := map[string]int{}
	var order []string
	for _, l := range lines {
		m := g.key.FindStringSubmatch(l)
		if len(m) < 2 {
			continue
		}
		k := m[1]
		if _, ok := total[k]; !ok {
			order = append(order, k)
		}
		total[k]++
	}
	if len(order) == 0 {
		return lines, false // no grep/rg-style lines: leave untouched
	}

	inCap := make(map[string]bool, len(order))
	for i, k := range order {
		inCap[k] = i < maxGroups
	}

	// Pass 2: rebuild, preserving original order within and across groups.
	truncated := false
	seen := make(map[string]int, len(order))
	dropped := map[string]bool{}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		m := g.key.FindStringSubmatch(l)
		if len(m) < 2 {
			out = append(out, l) // passthrough untouched
			continue
		}
		k := m[1]
		if !inCap[k] {
			dropped[k] = true
			truncated = true
			continue
		}
		n := seen[k]
		seen[k]++
		switch {
		case n < perGroup:
			out = append(out, l)
		case n == perGroup:
			out = append(out, fmt.Sprintf(g.omitLabel, total[k]-perGroup, k))
			truncated = true
		default:
			// already past the per-group cap and emitted the omit line; skip
		}
	}
	if len(dropped) > 0 {
		out = append(out, fmt.Sprintf("... (+%d more groups)", len(dropped)))
	}
	return out, truncated
}

func retain(lines []string, keep func(string) bool) []string {
	out := lines[:0]
	for _, l := range lines {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out
}

// splitLines splits on '\n', strips a trailing '\r' from newline-terminated
// lines, and yields nothing for an empty string.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		parts = parts[:len(parts)-1]
		for i := range parts {
			parts[i] = strings.TrimSuffix(parts[i], "\r")
		}
	} else {
		for i := 0; i < len(parts)-1; i++ {
			parts[i] = strings.TrimSuffix(parts[i], "\r")
		}
	}
	return parts
}

// truncate counts runes, appends "..." past maxLen, and returns "..." if
// maxLen < 3.
func truncate(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if maxLen < 3 {
		return "..."
	}
	return string(runes[:maxLen-3]) + "..."
}
