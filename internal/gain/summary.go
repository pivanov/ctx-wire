package gain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ctx-wire/internal/commandpolicy"
	"ctx-wire/internal/filter"
)

const (
	defaultMinOpportunityBytes = 1024
	// payloadHintMinOpportunityBytes is the emitted-byte floor for command-shape
	// payload hints such as "add -n to rg". Below this, the command is too small
	// to be worth interrupting the user with shape advice.
	payloadHintMinOpportunityBytes = 8 * 1024
	// payloadMinOpportunityBytes is the higher emitted-bytes floor a filtered,
	// low-savings payload-style command must clear before it counts as a token
	// opportunity. Their filters are deliberately conservative (the output is
	// mostly the content the agent wanted), so only genuinely large reads are
	// worth surfacing.
	payloadMinOpportunityBytes = 32 * 1024
)

// payloadPrograms are source/search and Unix-transform commands whose output is
// mostly payload the agent asked for, so their built-in filters keep content and
// save little by design. A low-savings filtered row for one of these is only an
// opportunity when the output is large. The transform family (sort/tr/cut/
// base64/xargs) emits a re-shaped copy of its input, which is still the content
// the agent wanted, so it is conservative payload rather than a weak filter.
var payloadPrograms = map[string]bool{
	"cat": true, "sed": true, "head": true, "tail": true,
	"nl": true, "rg": true, "grep": true,
	"find": true, "ls": true, "wc": true, "lsof": true,
	"strings": true, "agent-browser": true,
	"sort": true, "tr": true, "cut": true, "base64": true, "xargs": true,
}

// IsPayloadProgram reports whether prog (a program basename) is a payload-style
// source/search command. Exposed so the explain diagnostics stay in sync with
// the opportunity-classification rules here.
func IsPayloadProgram(prog string) bool { return payloadPrograms[prog] }

// payloadFilters are built-in filters whose output is mostly the source/diff/
// list/status content the agent asked for, so a low-savings filtered row is
// expected payload, not a weak filter. Keyed by filter name because the program
// ("git") is shared with non-payload git subcommands.
var payloadFilters = map[string]bool{
	"git-diff":      true,
	"git-status":    true,
	"git-list":      true,
	"git-log":       true,
	"inline-script": true,
	"shell-script":  true,
}

// awkPrograms are awk-family programs whose NR line-range/line-number reads are
// source payload (printing selected lines), as opposed to field transforms or
// summarization.
var awkPrograms = map[string]bool{"awk": true, "gawk": true, "mawk": true, "nawk": true}

// nrComparisonRE matches an awk record-number (NR) used directly in a relational
// comparison (NR>=507, NR<=620, NR==10). A modulo/arithmetic form like NR%2==0
// deliberately does not match; only plain line-range/line-number reads count.
var nrComparisonRE = regexp.MustCompile(`(^|[^A-Za-z0-9_])NR\s*(==|!=|>=|<=|>|<)|(==|!=|>=|<=|>|<)\s*NR([^A-Za-z0-9_]|$)`)

// awkPrintNRRE matches search/read awk snippets that print the original record
// with its record number, e.g. /term/{print NR": "$0}. That shape is equivalent
// to grep/rg -n payload. Field transforms still stay weak via awkTransformRE.
var awkPrintNRRE = regexp.MustCompile(`\bprint\b[^{};]*\bNR\b|\bNR\b[^{};]*\bprint\b`)

// awkTransformRE matches awk constructs that transform or summarize output:
// numbered/named/computed field references (anything but $0), BEGIN/END blocks,
// string functions, and accumulation operators. printf is intentionally allowed
// when the command is otherwise an NR/$0 line read, e.g. `printf "%6d\t%s", NR,
// $0`, because that is just numbered source payload.
var awkTransformRE = regexp.MustCompile(`\$[1-9]|\$\(|\$[A-Za-z_]|\bBEGIN\b|\bEND\b|\bnext\b|sprintf|sub\(|substr\(|split\(|length\(|gensub\(|\+\+|--|\+=|-=|\*=|/=`)

// awkScriptRE captures the awk program: the first single- or double-quoted
// argument. awk's program is conventionally quoted, e.g. `awk -F, '{print $1}'`,
// and gain re-quotes it the same way, so analyzing the quoted body (not the whole
// command line) keeps flags like `-F,` and file paths out of the inspection.
var awkScriptRE = regexp.MustCompile(`'([^']*)'|"([^"]*)"`)

// awkFieldPrintRE matches a print of a positional field ($1, $2, $NF), i.e. a
// column projection like `{print $2}` or `{print $1, $3}`. `$0` (the whole line)
// is deliberately excluded: it is handled as a line read, not a column extract.
var awkFieldPrintRE = regexp.MustCompile(`\bprint\b[^{}]*\$(?:[1-9][0-9]*|NF)\b`)

// awkComputeRE matches awk constructs that compute, aggregate, or reformat rather
// than simply project columns: blocks, control flow, the `in` operator, printf/
// string/math functions, increment/assignment, any arithmetic operator, and
// array subscripts. Its presence in the program body disqualifies an otherwise
// column-projection awk from being treated as conservative payload. It is run
// against the body with /regex/ and "string" literals stripped (awkLiteralRE) so
// a row selector like /error/ is not mistaken for a division operator.
var awkComputeRE = regexp.MustCompile(`\bBEGIN\b|\bEND\b|\bnext\b|\bfor\b|\bwhile\b|\bin\b|printf|sprintf|sub\(|gsub\(|substr\(|split\(|length\(|gensub\(|toupper\(|tolower\(|\+\+|--|[-+*/%]|=[^=]|\[`)

// awkLiteralRE matches awk /regex/ and "string" literals, removed before the
// awkComputeRE check so their delimiters and contents are not read as operators.
var awkLiteralRE = regexp.MustCompile(`/[^/]*/|"[^"]*"`)

// IsConservativePayload reports whether a filtered opportunity is source/read
// payload expected to stay large: a payload-style program (cat/rg/sort/cut/...),
// a payload filter (git-diff/status/log/list), an awk NR line read, or a simple
// awk column projection. It is the single source of truth shared by the gain
// opportunity threshold (here) and by explain.Classify, so the two never drift.
// sample is the scrubbed command.
func IsConservativePayload(program, filter, sample string) bool {
	return IsPayloadProgram(program) || payloadFilters[filter] ||
		isAwkLineRead(program, sample) || isAwkFieldExtract(program, sample)
}

func isAwkLineRead(program, sample string) bool {
	if !awkPrograms[program] {
		return false
	}
	sample = StripRunPrefix(sample)
	if sample == "" {
		return false
	}
	if !nrComparisonRE.MatchString(sample) && !awkPrintNRRE.MatchString(sample) {
		return false
	}
	return !awkTransformRE.MatchString(sample)
}

// isAwkFieldExtract reports whether sample is a simple awk column projection such
// as `awk '{print $2}'` or `awk -F, '{print $1, $3}'`: it prints positional
// fields and the program body contains no computation, aggregation, formatting,
// or control flow. Field extraction is column payload (like cut), not a weak
// filter. Aggregation/transform awk (`{sum+=$3} END{print sum}`, `NR%2==0`,
// printf, joins) is excluded by awkComputeRE and stays weak.
func isAwkFieldExtract(program, sample string) bool {
	if !awkPrograms[program] {
		return false
	}
	script := awkScript(StripRunPrefix(sample))
	if script == "" {
		return false
	}
	if !awkFieldPrintRE.MatchString(script) {
		return false
	}
	// Strip /regex/ and "string" literals before checking for computation so a row
	// selector like /error/ or a quoted separator is not read as an operator.
	residue := awkLiteralRE.ReplaceAllString(script, " ")
	return !awkComputeRE.MatchString(residue)
}

// awkScript returns the first single- or double-quoted argument of an awk command
// (its program body), or "" when none is present.
func awkScript(sample string) string {
	m := awkScriptRE.FindStringSubmatch(sample)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

// StripRunPrefix removes a leading "ctx-wire run" wrapper so the underlying
// command shape can be inspected. It understands ctx-wire run's wrapper flags
// because hooks/plugins usually emit "ctx-wire run --agent <agent> <cmd>" while
// gain records only the inner command.
func StripRunPrefix(s string) string {
	inner, ok := runPrefixInner(s)
	if !ok {
		return s
	}
	return inner
}

// HasRunPrefix reports whether s starts with a ctx-wire run wrapper.
func HasRunPrefix(s string) bool {
	_, ok := runPrefixInner(s)
	return ok
}

func runPrefixInner(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	const p = "ctx-wire run"
	if trimmed != p && !strings.HasPrefix(trimmed, p+" ") && !strings.HasPrefix(trimmed, p+"\t") {
		return s, false
	}
	rest := strings.TrimLeft(trimmed[len(p):], " \t")
	for {
		switch {
		case rest == "":
			return "", true
		case rest == "--no-dedup" || strings.HasPrefix(rest, "--no-dedup ") || strings.HasPrefix(rest, "--no-dedup\t"):
			rest = strings.TrimLeft(rest[len("--no-dedup"):], " \t")
		case rest == "--agent" || strings.HasPrefix(rest, "--agent ") || strings.HasPrefix(rest, "--agent\t"):
			rest = strings.TrimLeft(rest[len("--agent"):], " \t")
			rest = dropFirstToken(rest)
		case strings.HasPrefix(rest, "--agent="):
			rest = dropFirstToken(rest)
		case rest == "--shim" || strings.HasPrefix(rest, "--shim ") || strings.HasPrefix(rest, "--shim\t"):
			rest = strings.TrimLeft(rest[len("--shim"):], " \t")
		default:
			return strings.TrimSpace(rest), true
		}
	}
}

func dropFirstToken(s string) string {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return strings.TrimLeft(s[i:], " \t")
		}
	}
	return ""
}

// CommandStat aggregates entries grouped by program (first token of command).
type CommandStat struct {
	Program      string
	Count        int
	RawBytes     int64
	EmittedBytes int64
	SavedBytes   int64
}

// OpportunityStat aggregates command paths that still emit a lot of bytes.
type OpportunityStat struct {
	Program      string
	Mode         string
	Filter       string
	Sample       string // a representative scrubbed command from this group
	Kind         OpportunityKind
	Hint         string
	Count        int
	RawBytes     int64
	EmittedBytes int64
	SavedBytes   int64
}

// SavedPct is the share of raw bytes this opportunity saved, as a percentage.
// It is the single source of truth shared by explain and tune so the figure
// cannot drift between reports. Zero raw bytes yields 0 (no division by zero).
func (o OpportunityStat) SavedPct() float64 {
	if o.RawBytes <= 0 {
		return 0
	}
	return float64(o.SavedBytes) / float64(o.RawBytes) * 100
}

// OpportunityKind is the central policy classification for recorded command
// output. It determines whether a row is actionable enough for the gain table
// and how explain/tune should label it.
type OpportunityKind string

const (
	OpportunityIgnored         OpportunityKind = "ignored"
	OpportunityLowVolume       OpportunityKind = "low_volume"
	OpportunityWellFiltered    OpportunityKind = "well_filtered"
	OpportunityMissingFilter   OpportunityKind = "missing_filter"
	OpportunityWeakFilter      OpportunityKind = "weak_filter"
	OpportunityExpectedPayload OpportunityKind = "expected_payload"
	OpportunityHookLimited     OpportunityKind = "hook_limited"
)

// OpportunityDecision is the result of applying the central opportunity policy.
// Include means the row should be carried in Summary.Opportunities for explain/
// tune accounting. Actionable means the row should be visible in the gain table.
type OpportunityDecision struct {
	Kind       OpportunityKind
	Include    bool
	Actionable bool
	Reason     string
	Hint       string
}

// Summary is the aggregate over all recorded entries.
type Summary struct {
	Commands      int
	RawBytes      int64
	EmittedBytes  int64
	SavedBytes    int64
	ByProgram     []CommandStat     // sorted by SavedBytes descending
	ByAgent       []AgentStat       // sorted by SavedBytes desc, unattributed last
	BySource      []SourceStat      // sorted by SavedBytes desc, unattributed last
	Opportunities []OpportunityStat // sorted by EmittedBytes descending
}

// Options controls summary filtering.
type Options struct {
	Since               time.Time
	MinOpportunityBytes int64
	Agent               string // when set, keep only this invoking agent's entries
}

// SavingsPct returns the percentage of raw bytes saved (0 when no raw bytes).
func (s *Summary) SavingsPct() float64 {
	if s.RawBytes == 0 {
		return 0
	}
	return float64(s.SavedBytes) / float64(s.RawBytes) * 100
}

// Summarize reads the JSONL log and aggregates it. A missing log yields an empty
// summary. Malformed lines are skipped.
func Summarize() (*Summary, error) {
	return SummarizeWithOptions(Options{})
}

// SummarizeWithOptions reads the JSONL log and aggregates it with filters.
func SummarizeWithOptions(opts Options) (*Summary, error) {
	if opts.MinOpportunityBytes <= 0 {
		opts.MinOpportunityBytes = defaultMinOpportunityBytes
	}
	paths, err := gainReadPaths()
	if err != nil {
		return nil, err
	}
	sum := &Summary{}
	byProg := map[string]*CommandStat{}
	byOpp := map[string]*OpportunityStat{}
	byAgent := map[string]*AgentStat{}
	bySource := map[string]*SourceStat{}
	for _, path := range paths {
		if err := summarizePath(path, opts, sum, byProg, byOpp, byAgent, bySource); err != nil {
			return nil, err
		}
	}

	for _, st := range byProg {
		sum.ByProgram = append(sum.ByProgram, *st)
	}
	sort.Slice(sum.ByProgram, func(i, j int) bool {
		return sum.ByProgram[i].SavedBytes > sum.ByProgram[j].SavedBytes
	})
	for _, st := range byAgent {
		sum.ByAgent = append(sum.ByAgent, *st)
	}
	sort.Slice(sum.ByAgent, func(i, j int) bool {
		// Unattributed sinks last, then by savings, then by name for stability.
		if (sum.ByAgent[i].Agent == "") != (sum.ByAgent[j].Agent == "") {
			return sum.ByAgent[j].Agent == ""
		}
		if sum.ByAgent[i].SavedBytes != sum.ByAgent[j].SavedBytes {
			return sum.ByAgent[i].SavedBytes > sum.ByAgent[j].SavedBytes
		}
		return sum.ByAgent[i].Agent < sum.ByAgent[j].Agent
	})
	for _, st := range bySource {
		sum.BySource = append(sum.BySource, *st)
	}
	sort.Slice(sum.BySource, func(i, j int) bool {
		// Untagged (pre-source-tag) sinks last, then by savings, then by name.
		if (sum.BySource[i].Source == "") != (sum.BySource[j].Source == "") {
			return sum.BySource[j].Source == ""
		}
		if sum.BySource[i].SavedBytes != sum.BySource[j].SavedBytes {
			return sum.BySource[i].SavedBytes > sum.BySource[j].SavedBytes
		}
		return sum.BySource[i].Source < sum.BySource[j].Source
	})
	for _, st := range byOpp {
		sum.Opportunities = append(sum.Opportunities, *st)
	}
	sort.Slice(sum.Opportunities, func(i, j int) bool {
		return sum.Opportunities[i].EmittedBytes > sum.Opportunities[j].EmittedBytes
	})
	return sum, nil
}

func summarizePath(path string, opts Options, sum *Summary, byProg map[string]*CommandStat, byOpp map[string]*OpportunityStat, byAgent map[string]*AgentStat, bySource map[string]*SourceStat) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		// The fallback exists specifically for sandboxed agents. If one store is
		// unreadable in the current context, ignore it and summarize the rest.
		return nil
	}
	defer f.Close()

	if err := scanGainLines(f, func(line []byte) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return // skip malformed
		}
		if !opts.Since.IsZero() {
			ts, err := time.Parse(time.RFC3339, e.TS)
			if err != nil || ts.Before(opts.Since) {
				return
			}
		}
		if opts.Agent != "" && e.Agent != opts.Agent {
			return
		}
		prog := programName(e.Command)
		if prog == "ctx-wire" {
			return
		}
		sum.Commands++
		sum.RawBytes += int64(e.RawBytes)
		sum.EmittedBytes += int64(e.EmittedBytes)
		sum.SavedBytes += int64(e.SavedBytes)

		st := byProg[prog]
		if st == nil {
			st = &CommandStat{Program: prog}
			byProg[prog] = st
		}
		st.Count++
		st.RawBytes += int64(e.RawBytes)
		st.EmittedBytes += int64(e.EmittedBytes)
		st.SavedBytes += int64(e.SavedBytes)

		ast := byAgent[e.Agent]
		if ast == nil {
			ast = &AgentStat{Agent: e.Agent}
			byAgent[e.Agent] = ast
		}
		ast.Commands++
		ast.RawBytes += int64(e.RawBytes)
		ast.EmittedBytes += int64(e.EmittedBytes)
		ast.SavedBytes += int64(e.SavedBytes)

		sst := bySource[e.Source]
		if sst == nil {
			sst = &SourceStat{Source: e.Source}
			bySource[e.Source] = sst
		}
		sst.Commands++
		sst.RawBytes += int64(e.RawBytes)
		sst.EmittedBytes += int64(e.EmittedBytes)
		sst.SavedBytes += int64(e.SavedBytes)

		decision := OpportunityPolicyForEntry(e, opts.MinOpportunityBytes)
		if decision.Include {
			mode := e.Mode
			if mode == "" {
				mode = "unknown"
			}
			filter := e.Filter
			if filter == "" {
				filter = "-"
			}
			key := prog + "\x00" + mode + "\x00" + filter + "\x00" + string(decision.Kind) + "\x00" + decision.Hint
			opp := byOpp[key]
			if opp == nil {
				opp = &OpportunityStat{
					Program: prog,
					Mode:    mode,
					Filter:  filter,
					Sample:  e.Command,
					Kind:    decision.Kind,
					Hint:    decision.Hint,
				}
				byOpp[key] = opp
			}
			opp.Count++
			opp.RawBytes += int64(e.RawBytes)
			opp.EmittedBytes += int64(e.EmittedBytes)
			opp.SavedBytes += int64(e.SavedBytes)
		}
	}); err != nil {
		return err
	}
	return nil
}

// OpportunityPolicyForEntry applies the central opportunity policy to one gain
// log entry.
func OpportunityPolicyForEntry(e Entry, minEmittedBytes int64) OpportunityDecision {
	if minEmittedBytes <= 0 {
		minEmittedBytes = defaultMinOpportunityBytes
	}
	return classifyOpportunity(programName(e.Command), e.Mode, e.Filter, e.Command, int64(e.RawBytes), int64(e.EmittedBytes), int64(e.SavedBytes), minEmittedBytes)
}

// OpportunityPolicyForStat applies the central opportunity policy to an
// aggregated row. It recomputes from the representative sample so tests and
// downstream packages do not depend on Kind being pre-populated.
func OpportunityPolicyForStat(o OpportunityStat, minEmittedBytes int64) OpportunityDecision {
	if minEmittedBytes <= 0 {
		minEmittedBytes = defaultMinOpportunityBytes
	}
	prog := o.Program
	if prog == "" {
		prog = programName(o.Sample)
	}
	return classifyOpportunity(prog, o.Mode, o.Filter, o.Sample, o.RawBytes, o.EmittedBytes, o.SavedBytes, minEmittedBytes)
}

func classifyOpportunity(prog, mode, filter, sample string, rawBytes, emittedBytes, savedBytes, minEmittedBytes int64) OpportunityDecision {
	if rawBytes <= 0 || emittedBytes <= 0 {
		return OpportunityDecision{Kind: OpportunityIgnored, Reason: "empty byte totals"}
	}
	if prog == "ctx-wire" {
		return OpportunityDecision{Kind: OpportunityIgnored, Reason: "ctx-wire self command"}
	}
	if mode == "" {
		return OpportunityDecision{Kind: OpportunityIgnored, Reason: "missing runner mode"}
	}
	if int64(emittedBytes) < minEmittedBytes {
		return OpportunityDecision{Kind: OpportunityLowVolume, Reason: "below emitted-byte floor"}
	}
	if mode == "passthrough" && isHookLimitedSample(prog, sample) {
		return OpportunityDecision{
			Kind:    OpportunityHookLimited,
			Include: true,
			Reason:  "pipeline, redirection, or streaming/interactive command",
		}
	}
	// Passthrough commands (including payload-style ones) are surfaced at the
	// normal floor: a missing or non-matching filter is a real gap.
	if mode == "passthrough" {
		return OpportunityDecision{
			Kind:       OpportunityMissingFilter,
			Include:    true,
			Actionable: true,
			Reason:     "passthrough command above emitted-byte floor",
		}
	}
	// Filtered: only a low-savings row is worth flagging.
	lowSavings := savedBytes <= 0 || float64(savedBytes)/float64(rawBytes) < 0.10
	if !lowSavings {
		return OpportunityDecision{Kind: OpportunityWellFiltered, Reason: "filter saves enough"}
	}
	if IsConservativePayload(prog, filter, sample) {
		hint := TeeLogHint(sample)
		if hint == "" && emittedBytes >= payloadHintMinOpportunityBytes {
			hint = SearchLineNumberHint(prog, sample)
		}
		actionable := hint != "" || emittedBytes >= payloadMinOpportunityBytes
		return OpportunityDecision{
			Kind:       OpportunityExpectedPayload,
			Include:    actionable,
			Actionable: actionable,
			Reason:     "conservative payload",
			Hint:       hint,
		}
	}
	return OpportunityDecision{
		Kind:       OpportunityWeakFilter,
		Include:    true,
		Actionable: true,
		Reason:     "filtered command with low savings",
	}
}

// IsActionableOpportunity reports whether an aggregated opportunity should be
// printed in the gain Token Opportunities table.
func IsActionableOpportunity(o OpportunityStat) bool {
	return OpportunityPolicyForStat(o, defaultMinOpportunityBytes).Actionable
}

// CommandShapeHint returns a product-level hint for payload commands where the
// command shape, not the filter, is the thing to improve.
func CommandShapeHint(program, sample string) string {
	if hint := TeeLogHint(sample); hint != "" {
		return hint
	}
	return SearchLineNumberHint(program, sample)
}

// TeeLogHint returns a hint when a command reads ctx-wire's full tee/spool logs.
func TeeLogHint(sample string) string {
	if sample == "" {
		return ""
	}
	for _, tok := range strings.Fields(StripRunPrefix(sample)) {
		if isCtxWireTeeLogPath(tok) {
			return "this reads a full ctx-wire spool log; prefer head, tail, or sed -n START,ENDp, or inspect the file outside agent context"
		}
	}
	return ""
}

func isCtxWireTeeLogPath(tok string) bool {
	tok = strings.Trim(tok, `"'`)
	return strings.Contains(tok, "/.local/share/ctx-wire/tee/") ||
		strings.Contains(tok, "~/.local/share/ctx-wire/tee/") ||
		strings.Contains(tok, "$HOME/.local/share/ctx-wire/tee/")
}

// SearchLineNumberHint returns a hint for large search output that cannot be
// grouped by file because line numbers are missing.
func SearchLineNumberHint(program, sample string) string {
	if sample == "" || !isSearchProgram(program) {
		return ""
	}
	inner := StripRunPrefix(sample)
	if isSearchListingMode(program, inner) || hasSearchLineNumberFlag(inner) {
		return ""
	}
	return "for large search output, add -n/--line-number when safe so ctx-wire can group matches by file"
}

func isSearchProgram(prog string) bool {
	switch prog {
	case "rg", "ripgrep", "grep", "egrep", "fgrep", "ag", "ack":
		return true
	default:
		return false
	}
}

func hasSearchLineNumberFlag(sample string) bool {
	for _, tok := range strings.Fields(sample) {
		switch {
		case tok == "-n" || tok == "--line-number" || tok == "--vimgrep":
			return true
		case strings.HasPrefix(tok, "-") && strings.Contains(tok, "n") && !strings.HasPrefix(tok, "--"):
			return true
		}
	}
	return false
}

func isSearchListingMode(program, sample string) bool {
	if program != "rg" && program != "ripgrep" {
		return false
	}
	for _, tok := range strings.Fields(sample) {
		switch tok {
		case "--files", "-l", "--files-with-matches", "--files-without-match", "--count", "-c":
			return true
		}
	}
	return false
}

func isHookLimitedSample(program, sample string) bool {
	inner := StripRunPrefix(sample)
	if hasTopLevelPipeOrRedirect(inner) {
		return true
	}
	if isInteractiveProgram(program) {
		return true
	}
	for _, tok := range strings.Fields(inner) {
		if commandpolicy.IsStreamingArg(tok) {
			return true
		}
	}
	return false
}

func isInteractiveProgram(program string) bool {
	return commandpolicy.IsInteractiveProgram(program)
}

// ProgramName returns the gain report program bucket for a scrubbed command
// sample. Telemetry uses the same helper so per-program aggregate counters match
// `ctx-wire gain`.
func ProgramName(s string) string {
	return programName(s)
}

func hasTopLevelPipeOrRedirect(s string) bool {
	var quote rune
	escaped := false
	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '|' || r == '>' || r == '<' {
			return true
		}
	}
	return false
}

func programName(s string) string {
	// Peel a package-runner prefix so the real tool is attributed instead of the
	// wrapper (e.g. "bunx prettier" -> "prettier"). Keep the wrapper when the inner
	// token is a flag ("npx -y create-foo") or absent (a bare "bunx").
	if inner := filter.StripRunnerPrefix(s); inner != s {
		if t := firstToken(inner); t != "(none)" && t != "" && !strings.HasPrefix(t, "-") {
			s = inner
		}
	}
	token := firstToken(s)
	if token == "(none)" {
		return token
	}
	base := filepath.Base(token)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return token
	}
	return base
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	if s == "" {
		return "(none)"
	}
	return s
}
