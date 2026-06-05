package gain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"ctx-wire/internal/ui"
)

// Entries returns recorded gain entries in chronological (append) order,
// filtered to opts.Since. ctx-wire's own commands are skipped, matching the
// summary view. It powers the history/daily/graph/export views.
func Entries(opts Options) ([]Entry, error) {
	paths, err := gainReadPaths()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			continue // an unreadable store in a sandbox is skipped, not fatal
		}
		_ = scanGainLines(f, func(line []byte) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				return
			}
			var e Entry
			if json.Unmarshal(line, &e) != nil {
				return
			}
			if !opts.Since.IsZero() {
				ts, terr := time.Parse(time.RFC3339, e.TS)
				if terr != nil || ts.Before(opts.Since) {
					return
				}
			}
			if programName(e.Command) == "ctx-wire" {
				return
			}
			out = append(out, e)
		})
		f.Close()
	}
	// Entries are appended in path order (primary log family, then fallback), so
	// across two stores they are not chronological. Sort by parsed timestamp,
	// stably, matching RecentEntries, so --history --top N selects the newest
	// entries across all stores rather than the tail of whichever store is last.
	sort.SliceStable(out, func(i, j int) bool {
		ti, ei := time.Parse(time.RFC3339, out[i].TS)
		tj, ej := time.Parse(time.RFC3339, out[j].TS)
		if ei != nil || ej != nil {
			return i < j
		}
		return ti.Before(tj)
	})
	return out, nil
}

// AgentStat aggregates savings attributed to one agent. Agent is "" for
// commands recorded without attribution: pre-attribution data, or a command run
// in a plain shell rather than under a recognized agent.
type AgentStat struct {
	Agent        string `json:"agent"`
	Commands     int    `json:"commands"`
	RawBytes     int64  `json:"raw_bytes"`
	EmittedBytes int64  `json:"emitted_bytes"`
	SavedBytes   int64  `json:"saved_bytes"`
}

// SavingsPct is the percentage of raw bytes this agent saved (0 when no raw
// bytes).
func (a AgentStat) SavingsPct() float64 {
	if a.RawBytes == 0 {
		return 0
	}
	return float64(a.SavedBytes) / float64(a.RawBytes) * 100
}

// AgentTotals buckets entries by attributed agent, sorted by SavedBytes
// descending with the unattributed bucket ("") always last so a real split
// (e.g. claude vs codex) reads first. It powers the per-agent breakdown for
// quota and telemetry. ctx-wire's own commands are already excluded by Entries.
func AgentTotals(entries []Entry) []AgentStat {
	idx := map[string]*AgentStat{}
	var order []string
	for _, e := range entries {
		s := idx[e.Agent]
		if s == nil {
			s = &AgentStat{Agent: e.Agent}
			idx[e.Agent] = s
			order = append(order, e.Agent)
		}
		s.Commands++
		s.RawBytes += int64(e.RawBytes)
		s.EmittedBytes += int64(e.EmittedBytes)
		s.SavedBytes += int64(e.SavedBytes)
	}
	out := make([]AgentStat, 0, len(order))
	for _, a := range order {
		out = append(out, *idx[a])
	}
	sort.Slice(out, func(i, j int) bool {
		// The unattributed bucket sinks to the end regardless of size.
		if (out[i].Agent == "") != (out[j].Agent == "") {
			return out[j].Agent == ""
		}
		if out[i].SavedBytes != out[j].SavedBytes {
			return out[i].SavedBytes > out[j].SavedBytes
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// DailyStat aggregates one UTC calendar day of savings.
type DailyStat struct {
	Date         string `json:"date"`
	Commands     int    `json:"commands"`
	RawBytes     int64  `json:"raw_bytes"`
	EmittedBytes int64  `json:"emitted_bytes"`
	SavedBytes   int64  `json:"saved_bytes"`
}

// SavingsPct is the percentage of raw bytes saved on this day.
func (d DailyStat) SavingsPct() float64 {
	if d.RawBytes == 0 {
		return 0
	}
	return float64(d.SavedBytes) / float64(d.RawBytes) * 100
}

// groupByPeriod buckets entries by the (UTC) period label returned by key,
// sorted oldest first (labels are zero-padded so lexical order is chronological).
// Entries with an unparseable timestamp are skipped.
func groupByPeriod(entries []Entry, key func(time.Time) string) []DailyStat {
	idx := map[string]*DailyStat{}
	var order []string
	for _, e := range entries {
		ts, err := time.Parse(time.RFC3339, e.TS)
		if err != nil {
			continue
		}
		k := key(ts.UTC())
		d := idx[k]
		if d == nil {
			d = &DailyStat{Date: k}
			idx[k] = d
			order = append(order, k)
		}
		d.Commands++
		d.RawBytes += int64(e.RawBytes)
		d.EmittedBytes += int64(e.EmittedBytes)
		d.SavedBytes += int64(e.SavedBytes)
	}
	sort.Strings(order)
	out := make([]DailyStat, 0, len(order))
	for _, k := range order {
		out = append(out, *idx[k])
	}
	return out
}

// Daily groups entries by UTC date (YYYY-MM-DD), oldest first.
func Daily(entries []Entry) []DailyStat {
	return groupByPeriod(entries, func(t time.Time) string { return t.Format("2006-01-02") })
}

// Weekly groups entries by ISO week (YYYY-Www), oldest first.
func Weekly(entries []Entry) []DailyStat {
	return groupByPeriod(entries, func(t time.Time) string {
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	})
}

// Monthly groups entries by UTC month (YYYY-MM), oldest first.
func Monthly(entries []Entry) []DailyStat {
	return groupByPeriod(entries, func(t time.Time) string { return t.Format("2006-01") })
}

func pctText(pct float64) string {
	return fmt.Sprintf("%.1f%%", pct)
}

// FormatPeriodThemed renders a savings table grouped by period ("daily",
// "weekly", or "monthly"), oldest first.
func FormatPeriodThemed(period string, rows []DailyStat, theme ui.Theme) string {
	col := "Date"
	switch period {
	case "weekly":
		col = "Week"
	case "monthly":
		col = "Month"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.Heading("ctx-wire gain: "+period))
	if len(rows) == 0 {
		b.WriteString("no commands recorded yet\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%-12s %8s %10s %10s %7s\n", col, "Cmds", "Raw", "Saved", "Saved%")
	for _, d := range rows {
		fmt.Fprintf(&b, "%-12s %8d %10s %10s %7s\n",
			theme.Dim.Render(d.Date),
			d.Commands,
			ui.HumanBytes(d.RawBytes),
			theme.Number.Render(ui.HumanBytes(d.SavedBytes)),
			theme.Number.Render(pctText(d.SavingsPct())))
	}
	return b.String()
}

// FormatGraphThemed renders an ASCII bar graph of saved bytes per day.
func FormatGraphThemed(daily []DailyStat, theme ui.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.Heading("ctx-wire gain: daily saved bytes"))
	if len(daily) == 0 {
		b.WriteString("no commands recorded yet\n")
		return b.String()
	}
	var max int64 = 1
	for _, d := range daily {
		if d.SavedBytes > max {
			max = d.SavedBytes
		}
	}
	const width = 38
	for _, d := range daily {
		filled := int(int64(width) * d.SavedBytes / max)
		if filled < 0 {
			filled = 0
		}
		if filled > width {
			filled = width
		}
		bar := theme.OK.Render(strings.Repeat("█", filled)) + theme.Dim.Render(strings.Repeat("░", width-filled))
		fmt.Fprintf(&b, "%-12s %s %s\n", theme.Dim.Render(d.Date), bar, theme.Number.Render(ui.HumanBytes(d.SavedBytes)))
	}
	return b.String()
}

// FormatHistoryThemed renders the most recent entries (newest first), capped at
// limit (<=0 means 20).
func FormatHistoryThemed(entries []Entry, limit int, theme ui.Theme) string {
	if limit <= 0 {
		limit = 20
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.Heading("ctx-wire gain: history"))
	if len(entries) == 0 {
		b.WriteString("no commands recorded yet\n")
		return b.String()
	}
	start := len(entries) - limit
	if start < 0 {
		start = 0
	}
	for i := len(entries) - 1; i >= start; i-- {
		e := entries[i]
		when := e.TS
		if ts, err := time.Parse(time.RFC3339, e.TS); err == nil {
			when = ts.Local().Format("Jan 02 15:04")
		}
		mode := e.Mode
		if mode == "" {
			mode = "-"
		}
		fmt.Fprintf(&b, "%-13s %-10s %9s  %s\n",
			theme.Dim.Render(when),
			theme.Command.Render(programName(e.Command)),
			theme.Number.Render(ui.HumanBytes(int64(e.SavedBytes))),
			theme.Dim.Render(mode))
	}
	return b.String()
}

// jsonProgram and jsonExport shape the --json payload.
type jsonProgram struct {
	Program      string  `json:"program"`
	Count        int     `json:"count"`
	RawBytes     int64   `json:"raw_bytes"`
	EmittedBytes int64   `json:"emitted_bytes"`
	SavedBytes   int64   `json:"saved_bytes"`
	SavingsPct   float64 `json:"savings_pct"`
}

type jsonExport struct {
	Commands     int           `json:"commands"`
	RawBytes     int64         `json:"raw_bytes"`
	EmittedBytes int64         `json:"emitted_bytes"`
	SavedBytes   int64         `json:"saved_bytes"`
	SavedTokens  int64         `json:"saved_tokens"`
	SavingsPct   float64       `json:"savings_pct"`
	ByProgram    []jsonProgram `json:"by_program"`
	Daily        []DailyStat   `json:"daily"`
}

// FormatJSON renders the summary plus daily breakdown as a JSON object.
func FormatJSON(s *Summary, daily []DailyStat) (string, error) {
	out := jsonExport{
		Commands:     s.Commands,
		RawBytes:     s.RawBytes,
		EmittedBytes: s.EmittedBytes,
		SavedBytes:   s.SavedBytes,
		SavedTokens:  approxTokens(s.SavedBytes),
		SavingsPct:   s.SavingsPct(),
		Daily:        daily,
	}
	for _, st := range s.ByProgram {
		pct := 0.0
		if st.RawBytes > 0 {
			pct = float64(st.SavedBytes) / float64(st.RawBytes) * 100
		}
		out.ByProgram = append(out.ByProgram, jsonProgram{
			Program:      st.Program,
			Count:        st.Count,
			RawBytes:     st.RawBytes,
			EmittedBytes: st.EmittedBytes,
			SavedBytes:   st.SavedBytes,
			SavingsPct:   pct,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

// FormatCSV renders the daily breakdown as CSV (header + one row per day).
func FormatCSV(daily []DailyStat) string {
	var b strings.Builder
	b.WriteString("date,commands,raw_bytes,emitted_bytes,saved_bytes,savings_pct\n")
	for _, d := range daily {
		fmt.Fprintf(&b, "%s,%d,%d,%d,%d,%.1f\n",
			d.Date, d.Commands, d.RawBytes, d.EmittedBytes, d.SavedBytes, d.SavingsPct())
	}
	return b.String()
}
