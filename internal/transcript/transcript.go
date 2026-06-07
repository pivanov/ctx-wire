// Package transcript reads local agent transcripts (Claude Code today, Codex as
// a fast-follow) and yields executed commands paired with their output. The
// command and the output are scrubbed at this boundary, so every consumer gets
// secret-scrubbed data by construction. It is read-only and local-only.
//
// learn has its own (older, classify-only) Claude parser; migrating it onto this
// reader is a deliberate follow-up, kept out of this change to limit risk.
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ctx-wire/internal/scrub"
)

// defaultMaxOutput bounds retained per-command output when Options.MaxOutput is
// 0. It is larger than learn's classify-only cap, because a drafter wants the
// noisy body, but still bounded so a runaway transcript line cannot blow up
// memory.
const defaultMaxOutput = 256 << 10

// Exec is one executed command together with its output. Command and Output are
// already scrubbed.
type Exec struct {
	Command string
	Output  string
	IsError bool
	When    time.Time
	Agent   string
}

// Options scopes a scan.
type Options struct {
	Since      time.Time
	Project    string   // absolute project root; "" scans every project
	ClaudeDirs []string // Claude config dirs (each holds a projects/ subdir)
	MaxOutput  int      // per-output byte cap; 0 uses defaultMaxOutput
}

// Reader yields executed commands (with output) from one agent's transcripts.
// Claude is the first implementation; Codex slots in as a second without
// changing consumers.
type Reader interface {
	Name() string
	Execs(opts Options) ([]Exec, error)
}

// Claude reads Claude Code JSONL transcripts.
type Claude struct{}

// Name reports the agent this reader covers.
func (Claude) Name() string { return "claude" }

// Execs walks the configured Claude transcript dirs and returns executed Bash
// commands paired with their (scrubbed) output, in issue order. Best-effort: an
// unreadable file or dir is skipped, not fatal.
func (Claude) Execs(opts Options) ([]Exec, error) {
	max := opts.MaxOutput
	if max <= 0 {
		max = defaultMaxOutput
	}
	var out []Exec
	for _, base := range opts.ClaudeDirs {
		root := filepath.Join(base, "projects")
		if opts.Project != "" {
			root = filepath.Join(root, EncodeClaudeProjectSlug(opts.Project))
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			out = append(out, parseClaudeSession(path, opts.Since, max)...)
			return nil
		})
	}
	return out, nil
}

// claudeRecord is one transcript JSONL line: an assistant message (whose content
// may hold a tool_use Bash command) or a user message (whose content may hold
// the matching tool_result with is_error and output).
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
// tool_use with its later tool_result by tool_use_id. Command and output are
// scrubbed; output is capped at max bytes.
func parseClaudeSession(path string, since time.Time, max int) []Exec {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var execs []Exec
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
						execs = append(execs, Exec{
							Command: scrub.Scrub(strings.TrimSpace(c.Input.Command)),
							When:    ts,
							Agent:   "claude",
						})
					case c.Type == "tool_result" && c.ToolUseID != "":
						if idx, ok := pending[c.ToolUseID]; ok {
							execs[idx].IsError = c.IsError
							// Scrub the FULL output first, then cap: capping before
							// scrubbing could slice a secret at the boundary into a
							// partial token that no longer matches, leaking it.
							execs[idx].Output = clip(scrub.Scrub(decodeContent(c.Content)), max)
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
// It returns the full text; the caller scrubs and then caps it (capping here
// would risk slicing a secret before scrub can match it).
func decodeContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
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
		return b.String()
	}
	return ""
}

// EncodeClaudeProjectSlug reproduces Claude Code's project-directory slug: every
// '/', '.', '_', '\\', ' ', '[', ']' and any non-ASCII rune becomes '-'. So
// /Users/x/ctx-wire becomes -Users-x-ctx-wire. This is the canonical copy of the
// convention; discover and learn delegate here.
func EncodeClaudeProjectSlug(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for _, r := range path {
		switch {
		case r > 127:
			b.WriteByte('-')
		case r == '/' || r == '.' || r == '_' || r == '\\' || r == ' ' || r == '[' || r == ']':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clip(s string, max int) string {
	if max > 0 && len(s) > max {
		return s[:max]
	}
	return s
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
