// Package learn mines local Claude Code transcripts for repeated CLI mistakes:
// a command that failed, followed shortly by a corrected command that worked. It
// turns those into deduplicated correction rules the agent can be reminded of
// (written to .claude/rules/cli-corrections.md). It is read-only against
// transcripts and scrubs every command and error snippet before it is shown or
// persisted, so secrets never leak into a report or a rules file.
package learn

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ctx-wire/internal/discover"
	"ctx-wire/internal/scrub"
)

// maxOutputSample bounds how much tool-result text is retained per command. It
// is plenty to classify an error and far below a runaway transcript line.
const maxOutputSample = 4 << 10

// Execution is one Bash command from a transcript together with its result. The
// Command and Output are already scrubbed.
type Execution struct {
	Command string
	Output  string
	IsError bool
	TS      time.Time
	Agent   string
}

// Options controls a learn scan. ClaudeDirs and Project mirror discover so the
// scan can be scoped to the current project's sessions.
type Options struct {
	Since          time.Time
	Project        string // absolute project root; "" scans every project
	ClaudeDirs     []string
	MinOccurrences int // minimum times a correction must recur to be reported (default 1)
}

// Report is the analyzed result: deduplicated correction rules plus scan
// coverage for an honest summary.
type Report struct {
	Rules    []CorrectionRule
	Files    int // transcript files scanned
	Sessions int // sessions with at least one command
	Pairs    int // raw correction pairs before aggregation
}

// Analyze scans the configured Claude transcripts and returns correction rules.
func Analyze(opts Options) (*Report, error) {
	rep := &Report{}
	var pairs []CorrectionPair
	for _, base := range opts.ClaudeDirs {
		root := filepath.Join(base, "projects")
		if opts.Project != "" {
			root = filepath.Join(root, discover.EncodeClaudeProjectSlug(opts.Project))
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			rep.Files++
			session := parseClaudeSession(path, opts.Since)
			if len(session) == 0 {
				return nil
			}
			rep.Sessions++
			pairs = append(pairs, findCorrections(session)...)
			return nil
		})
	}
	rep.Pairs = len(pairs)
	rep.Rules = aggregate(pairs, opts.MinOccurrences)
	return rep, nil
}

// claudeRecord is one transcript JSONL line: either an assistant message (whose
// content may hold a tool_use Bash command) or a user message (whose content may
// hold the matching tool_result with is_error and output).
type claudeRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content []claudeContent `json:"content"`
	} `json:"message"`
}

type claudeContent struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	ID        string `json:"id"`          // tool_use id
	ToolUseID string `json:"tool_use_id"` // tool_result back-reference
	IsError   bool   `json:"is_error"`    // tool_result
	Input     struct {
		Command string `json:"command"`
	} `json:"input"` // tool_use
	Content json.RawMessage `json:"content"` // tool_result body (string or array)
}

// parseClaudeSession reads one transcript file in order, pairing each Bash
// tool_use with its later tool_result by tool_use_id, and returns the executions
// in issue order. Commands and outputs are scrubbed.
func parseClaudeSession(path string, since time.Time) []Execution {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var execs []Execution
	pending := map[string]int{} // tool_use_id -> index into execs

	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadString('\n')
		if s := strings.TrimSpace(line); s != "" {
			var rec claudeRecord
			if json.Unmarshal([]byte(s), &rec) == nil {
				ts := parseTS(rec.Timestamp)
				for _, c := range rec.Message.Content {
					switch {
					case c.Type == "tool_use" && c.Name == "Bash" && strings.TrimSpace(c.Input.Command) != "":
						if !since.IsZero() && !ts.IsZero() && ts.Before(since) {
							continue
						}
						pending[c.ID] = len(execs)
						execs = append(execs, Execution{
							Command: scrub.Scrub(strings.TrimSpace(c.Input.Command)),
							TS:      ts,
							Agent:   "claude",
						})
					case c.Type == "tool_result" && c.ToolUseID != "":
						if idx, ok := pending[c.ToolUseID]; ok {
							execs[idx].IsError = c.IsError
							execs[idx].Output = scrub.Scrub(decodeContent(c.Content))
							delete(pending, c.ToolUseID)
						}
					}
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return execs
}

// decodeContent flattens a tool_result content field, which Claude encodes
// either as a plain string or as an array of {type:"text", text:"..."} blocks.
// The result is truncated to maxOutputSample.
func decodeContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return truncate(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if bl.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(bl.Text)
			}
		}
		return truncate(b.String())
	}
	return ""
}

func truncate(s string) string {
	if len(s) <= maxOutputSample {
		return s
	}
	return s[:maxOutputSample]
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}
