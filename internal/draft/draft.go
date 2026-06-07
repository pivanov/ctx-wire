// Package draft turns a real captured command sample into a starter filter: a
// conservative match regex, content-agnostic transforms, and an inline test
// seeded from the (already scrubbed) sample. It is the pure core of
// `ctx-wire tune draft`; sourcing the sample and writing files live in the CLI.
//
// Safety by construction: every transform it infers is content-agnostic
// (blank-line / line-length / line-count), never keyed on the sample's bytes, so
// a generated strip pattern can never encode a secret. Structured JSON is never
// given a line-truncating transform (that would reintroduce mid-JSON corruption).
package draft

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/transcript"
)

// Target selects where a draft lands, which controls schema_version: a local or
// standalone file must carry schema_version = 1 (the registry rejects it
// otherwise), while a built-in omits it (concatBuiltins supplies the header).
type Target int

const (
	Local Target = iota
	Builtin
)

// Thresholds for the content-agnostic transforms. They mirror the shape of the
// existing built-ins (git-status, jq): trim only when the sample is genuinely
// noisy, and to a cap that still leaves useful output.
const (
	manyLinesThreshold = 40  // suggest max_lines above this many lines
	maxLinesValue      = 40  // ... capping to this
	longLineThreshold  = 200 // suggest truncate_lines_at above this width
	truncateValue      = 200 // ... truncating to this
)

// Spec is an inferred draft filter.
type Spec struct {
	Name         string
	Program      string
	MatchCommand string
	StripANSI    bool
	StripLines   []string
	TruncateAt   *int
	MaxLines     *int
	ReduceJSON   bool
	IsJSON       bool   // the sample is a single complete JSON document
	SampleWhen   string // RFC3339 of the seeding invocation, for the test name
}

// SelectSamples returns the execs whose command runs program, ordered by output
// size (largest first), so the default sample is the noisiest one and --sample N
// can pick the Nth. Output size is the ranking key because an escaped command
// has no gain record to rank by.
func SelectSamples(execs []transcript.Exec, program string) []transcript.Exec {
	var matches []transcript.Exec
	for _, e := range execs {
		if firstToken(e.Command) == program && e.Output != "" {
			matches = append(matches, e)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return len(matches[i].Output) > len(matches[j].Output)
	})
	return matches
}

// Matcher reports whether a command already matches a filter, meaning its
// transcript output was reduced rather than raw. *filter.Registry satisfies it.
type Matcher interface {
	Find(command string) *filter.CompiledFilter
}

// RawSamples keeps only samples whose command does not already match a filter,
// so a draft always seeds from raw (unfiltered) output: drafting from a command
// that was already filtered would learn the wrong shape. A nil matcher is a
// no-op (every sample kept).
//
// The matcher is the current registry, a proxy for "is this command filtered".
// It cannot reconstruct the filter state at the time the transcript was recorded
// if filters have changed since, which is acceptable here: a command covered now
// needs no new draft, and one not covered now is what we want to draft for.
func RawSamples(samples []transcript.Exec, m Matcher) []transcript.Exec {
	if m == nil {
		return samples
	}
	var raw []transcript.Exec
	for _, s := range samples {
		if m.Find(s.Command) == nil {
			raw = append(raw, s)
		}
	}
	return raw
}

// Infer builds a draft spec from a program, the seeding command, the scrubbed
// sample output, and the sample's timestamp.
func Infer(program, command, sample, when string) Spec {
	s := Spec{
		Program:      program,
		Name:         filterName(program, command),
		MatchCommand: matchCommand(program, command),
		StripANSI:    true, // always safe: terminal control codes are pure noise
		SampleWhen:   when,
	}
	if filter.IsCompleteJSON(sample) {
		// Structured data: jsonGuard already passes whole JSON through. Never add
		// a line-truncating transform here (the issue-#1 corruption class).
		s.IsJSON = true
		return s
	}
	lines := strings.Split(strings.TrimRight(sample, "\n"), "\n")
	if hasBlankLine(lines) {
		s.StripLines = []string{`^\s*$`}
	}
	if len(lines) > manyLinesThreshold {
		v := maxLinesValue
		s.MaxLines = &v
	}
	if longestLine(lines) > longLineThreshold {
		v := truncateValue
		s.TruncateAt = &v
	}
	return s
}

// FilterSpec converts a draft spec into the filter package's compile input, so
// the caller can apply the candidate filter to the sample for the expected
// output and the savings preview.
func (s Spec) FilterSpec() filter.DraftSpec {
	return filter.DraftSpec{
		MatchCommand:       s.MatchCommand,
		StripANSI:          s.StripANSI,
		StripLinesMatching: s.StripLines,
		TruncateLinesAt:    s.TruncateAt,
		MaxLines:           s.MaxLines,
		ReduceJSON:         s.ReduceJSON,
	}
}

// TOML renders the draft as a flat filter file (`[filters.<name>]` /
// `[[tests.<name>]]` directly, matching the built-ins and so safe to append).
// String values are quoted via the toml encoder, so any sample content escapes
// correctly. The sample is the test input; expected is the filter's output on it
// (it mirrors the filter, hence draft = true). target controls schema_version.
func (s Spec) TOML(sample, expected string, target Target) (string, error) {
	var b strings.Builder
	b.WriteString("# Drafted by `ctx-wire tune draft`. Trim `expected` to the intended output\n")
	b.WriteString("# and remove `draft = true`, then `ctx-wire verify --project`.\n")
	if s.IsJSON {
		b.WriteString("# The sample is complete JSON: jsonGuard passes whole JSON through, so no\n")
		b.WriteString("# line-truncating transform was added.\n")
	}
	b.WriteString("\n")
	if target == Local {
		b.WriteString("schema_version = 1\n\n")
	}

	fmt.Fprintf(&b, "[filters.%s]\n", s.Name)
	fmt.Fprintf(&b, "description = %s\n", tomlString(fmt.Sprintf("Draft: compact %s output", s.Program)))
	fmt.Fprintf(&b, "match_command = %s\n", tomlString(s.MatchCommand))
	if s.StripANSI {
		b.WriteString("strip_ansi = true\n")
	}
	if len(s.StripLines) > 0 {
		parts := make([]string, len(s.StripLines))
		for i, p := range s.StripLines {
			parts[i] = tomlString(p)
		}
		fmt.Fprintf(&b, "strip_lines_matching = [%s]\n", strings.Join(parts, ", "))
	}
	if s.TruncateAt != nil {
		fmt.Fprintf(&b, "truncate_lines_at = %d\n", *s.TruncateAt)
	}
	if s.MaxLines != nil {
		fmt.Fprintf(&b, "max_lines = %d\n", *s.MaxLines)
	}
	if s.ReduceJSON {
		b.WriteString("reduce_json = true\n")
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "[[tests.%s]]\n", s.Name)
	fmt.Fprintf(&b, "name = %s\n", tomlString("draft from claude transcript "+s.SampleWhen))
	b.WriteString("draft = true\n")
	fmt.Fprintf(&b, "input = %s\n", tomlString(sample))
	fmt.Fprintf(&b, "expected = %s\n", tomlString(expected))
	return b.String(), nil
}

// tomlString returns a TOML-quoted, fully escaped representation of s by letting
// the toml encoder quote a single value, then taking the value part. This keeps
// all escaping (newlines, quotes, backslashes) correct for arbitrary samples.
func tomlString(s string) string {
	var b strings.Builder
	_ = toml.NewEncoder(&b).Encode(map[string]string{"v": s})
	out := strings.TrimSpace(b.String())
	return strings.TrimSpace(strings.TrimPrefix(out, "v ="))
}

// HasFilter reports whether a TOML document already defines [filters.<name>], so
// --write can refuse or suffix instead of appending a duplicate table (which is
// a TOML parse error that would break every filter in the file).
func HasFilter(content, name string) bool {
	var f struct {
		Filters map[string]toml.Primitive `toml:"filters"`
	}
	if _, err := toml.Decode(content, &f); err != nil {
		return false
	}
	_, ok := f.Filters[name]
	return ok
}

// Savings reports the byte reduction of expected vs sample, the same basis gain
// reports, so the preview number matches telemetry.
func Savings(sample, expected string) (raw, emitted int, pct float64) {
	raw, emitted = len(sample), len(expected)
	if raw > 0 {
		pct = 100 * float64(raw-emitted) / float64(raw)
	}
	return
}

// firstToken returns the program name of a command: the basename of its first
// whitespace-delimited token (so "/usr/bin/git status" -> "git").
func firstToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	t := fields[0]
	if i := strings.LastIndexAny(t, "/\\"); i >= 0 {
		t = t[i+1:]
	}
	return t
}

var bareWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// subcommand returns a stable first subcommand (a bare lowercase word, not a
// flag or path) for a tighter regex, or "" when there is none.
func subcommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return ""
	}
	if bareWord.MatchString(fields[1]) {
		return fields[1]
	}
	return ""
}

// matchCommand derives a conservative, regex-escaped match_command:
// ^<program>\b, narrowed to ^<program> <sub>\b when a stable subcommand exists.
func matchCommand(program, command string) string {
	prog := regexp.QuoteMeta(program)
	if sub := subcommand(command); sub != "" {
		return fmt.Sprintf("^%s %s\\b", prog, regexp.QuoteMeta(sub))
	}
	return fmt.Sprintf("^%s\\b", prog)
}

// filterName derives a deterministic TOML bare key from program (+ subcommand):
// "git status" -> "git-status".
func filterName(program, command string) string {
	name := program
	if sub := subcommand(command); sub != "" {
		name = program + "-" + sub
	}
	return sanitizeKey(name)
}

func sanitizeKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func hasBlankLine(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			return true
		}
	}
	return false
}

func longestLine(lines []string) int {
	n := 0
	for _, l := range lines {
		if len(l) > n {
			n = len(l)
		}
	}
	return n
}
