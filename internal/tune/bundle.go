package tune

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"sort"
	"time"

	"ctx-wire/internal/explain"
	"ctx-wire/internal/gain"
)

// BundleMeta is the injected metadata for a bundle. Timestamps and environment
// details are passed in (not read from the clock, OS, or network) so BuildBundle
// is a pure, deterministic transform: identical inputs produce byte-identical
// archives, which keeps it testable.
type BundleMeta struct {
	GeneratedAt      time.Time
	Version          string
	Commit           string
	OS               string
	Arch             string
	FilterCount      int
	ConformanceCount int
	Window           string // human description of the time window
}

// bundleFiles is the fixed file order inside the archive. Kept stable for
// deterministic output and matches the Phase 3 manifest.
type bundleFile struct {
	name string
	data []byte
}

type bundleSummary struct {
	GeneratedAt      string       `json:"generated_at"`
	Version          string       `json:"ctx_wire_version,omitempty"`
	Commit           string       `json:"ctx_wire_commit,omitempty"`
	OS               string       `json:"os,omitempty"`
	Arch             string       `json:"arch,omitempty"`
	FilterCount      int          `json:"filter_count,omitempty"`
	ConformanceTests int          `json:"conformance_tests,omitempty"`
	Window           string       `json:"window"`
	Commands         int          `json:"commands"`
	RawBytes         int64        `json:"raw_bytes"`
	EmittedBytes     int64        `json:"emitted_bytes"`
	SavedBytes       int64        `json:"saved_bytes"`
	SavedPct         float64      `json:"saved_pct"`
	TopClasses       []classCount `json:"top_classes"`
}

type classCount struct {
	Class        string `json:"class"`
	Groups       int    `json:"groups"`
	Executions   int    `json:"executions"`
	EmittedBytes int64  `json:"emitted_bytes"`
}

type sampleRecord struct {
	Command      string  `json:"command"` // sanitized
	Program      string  `json:"program"`
	Mode         string  `json:"mode"`
	Filter       string  `json:"filter"`
	EmittedBytes int64   `json:"emitted_bytes"`
	SavedPct     float64 `json:"saved_pct"`
	Class        string  `json:"class"`
	Suggestion   string  `json:"suggestion,omitempty"`
}

type suggestionsFile struct {
	GeneratedAt string             `json:"generated_at"`
	Suggestions []suggestionRecord `json:"suggestions"`
}

type suggestionRecord struct {
	Program      string  `json:"program"`
	Class        string  `json:"class"`
	Suggestion   string  `json:"suggestion"`
	Sample       string  `json:"sample"` // sanitized
	EmittedBytes int64   `json:"emitted_bytes"`
	SavedPct     float64 `json:"saved_pct"`
	Count        int     `json:"count"`
}

// privacyReport is the static privacy disclosure shipped in every bundle. It
// must stay in sync with the Sanitizer rules and the exclusions below.
const privacyReport = `ctx-wire tune bundle: privacy report

Included:
  - summary.json: aggregate counts, byte totals, time window, ctx-wire version, OS/arch.
  - report.txt: the human-readable ctx-wire tune report (samples sanitized).
  - suggestions.json: per-row filter suggestions (samples sanitized).
  - samples/commands.jsonl: sanitized sample commands, one JSON object per line.
  - privacy_report.txt: this file.

Excluded:
  - Raw command output. No command output is captured or exported in this phase.
  - Process environment variables.
  - Full raw gain logs.

Sanitizer rules applied to every exported command:
  - Secrets are scrubbed (scrub.Scrub).
  - The user home directory is replaced with $HOME.
  - The current project root is replaced with $PROJECT.
  - Long absolute paths are compacted, keeping the trailing segments.
  - Sample command length is capped.

No network calls were made.
`

// BuildBundle assembles a deterministic .tar.gz archive of the sanitized tune
// data. It writes no files and makes no network calls; the caller persists the
// returned bytes. Command samples are classified on the original (so the
// classification matches ctx-wire explain/tune) but sanitized for export.
func BuildBundle(s *gain.Summary, san Sanitizer, opts Options, meta BundleMeta) ([]byte, error) {
	summaryJSON, err := buildSummaryJSON(s, meta)
	if err != nil {
		return nil, err
	}
	suggestionsJSON, err := buildSuggestionsJSON(s, san, meta)
	if err != nil {
		return nil, err
	}
	samplesJSONL, err := buildSamplesJSONL(s, san)
	if err != nil {
		return nil, err
	}

	files := []bundleFile{
		{"summary.json", summaryJSON},
		{"report.txt", []byte(buildReportTxt(s, san, opts))},
		{"suggestions.json", suggestionsJSON},
		{"samples/commands.jsonl", samplesJSONL},
		{"privacy_report.txt", []byte(privacyReport)},
	}

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	// Zero the gzip header time/name so the wrapper is deterministic; per-entry
	// time lives in the tar headers below (from injected meta).
	gw.ModTime = time.Time{}
	gw.Name = ""
	tw := tar.NewWriter(gw)
	for _, f := range files {
		hdr := &tar.Header{
			Name:     f.name,
			Mode:     0o644,
			Size:     int64(len(f.data)),
			ModTime:  meta.GeneratedAt,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildSummaryJSON(s *gain.Summary, meta BundleMeta) ([]byte, error) {
	sum := bundleSummary{
		GeneratedAt:      meta.GeneratedAt.UTC().Format(time.RFC3339),
		Version:          meta.Version,
		Commit:           meta.Commit,
		OS:               meta.OS,
		Arch:             meta.Arch,
		FilterCount:      meta.FilterCount,
		ConformanceTests: meta.ConformanceCount,
		Window:           meta.Window,
		Commands:         s.Commands,
		RawBytes:         s.RawBytes,
		EmittedBytes:     s.EmittedBytes,
		SavedBytes:       s.SavedBytes,
		SavedPct:         s.SavingsPct(),
		TopClasses:       topClasses(s),
	}
	return json.MarshalIndent(sum, "", "  ")
}

func topClasses(s *gain.Summary) []classCount {
	agg := map[string]*classCount{}
	var order []string
	for _, o := range s.Opportunities {
		label := classLabel(o)
		c := agg[label]
		if c == nil {
			c = &classCount{Class: label}
			agg[label] = c
			order = append(order, label)
		}
		c.Groups++
		c.Executions += o.Count
		c.EmittedBytes += o.EmittedBytes
	}
	out := make([]classCount, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EmittedBytes != out[j].EmittedBytes {
			return out[i].EmittedBytes > out[j].EmittedBytes
		}
		return out[i].Class < out[j].Class
	})
	return out
}

func buildSamplesJSONL(s *gain.Summary, san Sanitizer) ([]byte, error) {
	var buf bytes.Buffer
	for _, o := range s.Opportunities {
		cls := explain.Classify(o)
		line, err := json.Marshal(sampleRecord{
			Command:      san.Sample(o.Sample),
			Program:      o.Program,
			Mode:         o.Mode,
			Filter:       o.Filter,
			EmittedBytes: o.EmittedBytes,
			SavedPct:     o.SavedPct(),
			Class:        classLabel(o),
			Suggestion:   suggestionFor(cls),
		})
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func buildSuggestionsJSON(s *gain.Summary, san Sanitizer, meta BundleMeta) ([]byte, error) {
	out := suggestionsFile{GeneratedAt: meta.GeneratedAt.UTC().Format(time.RFC3339)}
	for _, o := range s.Opportunities {
		cls := explain.Classify(o)
		if _, actionable := sectionFor(cls); !actionable {
			continue
		}
		out.Suggestions = append(out.Suggestions, suggestionRecord{
			Program:      o.Program,
			Class:        classLabel(o),
			Suggestion:   suggestionFor(cls),
			Sample:       san.Sample(o.Sample),
			EmittedBytes: o.EmittedBytes,
			SavedPct:     o.SavedPct(),
			Count:        o.Count,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// buildReportTxt renders the human report with sanitized samples. Classification
// happens in Analyze on the original summary; only the displayed sample strings
// are sanitized, so the report's grouping matches ctx-wire tune exactly.
func buildReportTxt(s *gain.Summary, san Sanitizer, opts Options) string {
	rep := Analyze(nil, s)
	sanitizeReport(&rep, san)
	return Format(rep, opts)
}

func sanitizeReport(r *Report, san Sanitizer) {
	for sec, rows := range r.Sections {
		for i := range rows {
			rows[i].Sample = san.Sample(rows[i].Sample)
		}
		r.Sections[sec] = rows
	}
	for i := range r.ShapeHints {
		r.ShapeHints[i].Sample = san.Sample(r.ShapeHints[i].Sample)
	}
}
