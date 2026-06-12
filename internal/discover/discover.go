// Package discover cross-references what an AI agent actually executed (from the
// agent's own local session transcripts) against what ctx-wire recorded in its
// gain log. Commands that the agent ran but ctx-wire never saw are "escaped":
// the hook did not fire, the command ran raw, or it went through another tool.
//
// gain only ever sees commands that went through `ctx-wire run`, so a healthy
// gain report can hide a whole class of waste. discover closes that blind spot.
// It is strictly read-only and local-only: it reads transcripts and the gain
// log, makes no network calls, and writes nothing.
//
// Matching is deliberately conservative. A scrubbed-command match against a gain
// record is treated as "possibly captured", and the absence of any matching gain
// record for a command ctx-wire would have filtered is treated as "escaped". We
// do not pretend perfect correlation between transcript timestamps and gain
// records.
package discover

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ctx-wire/internal/explain"
	"ctx-wire/internal/filter"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/hook"
	"ctx-wire/internal/scrub"
	"ctx-wire/internal/transcript"
)

// Category is the classification of one executed command.
type Category string

const (
	// CatCaptured: ctx-wire would filter this and a matching gain record exists
	// (reported as "possibly captured" given imperfect correlation).
	CatCaptured Category = "captured"
	// CatEscaped: ctx-wire would have filtered this, but no gain record matched.
	// The actionable blind spot: hook missed, ran raw, or via another tool.
	CatEscaped Category = "escaped"
	// CatPassthrough: a command ctx-wire passes through by design (pipeline,
	// redirection, subshell, builtin, already-wrapped).
	CatPassthrough Category = "passthrough"
	// CatHookLimited: wrapped but the runner bypasses it (interactive/streaming).
	CatHookLimited Category = "hook_limited"
	// CatPredatesLedger: the command ran before the gain ledger's earliest record,
	// so no gain record could ever match it. Bucketed apart from escaped so a short
	// ledger against a long transcript history does not masquerade as waste.
	CatPredatesLedger Category = "predates_ledger"
	// CatUnknown: could not be classified confidently.
	CatUnknown Category = "unknown"
)

// catOrder fixes the report order: the actionable escaped class first.
var catOrder = []Category{CatEscaped, CatCaptured, CatPassthrough, CatHookLimited, CatPredatesLedger, CatUnknown}

// ledgerGrace is how far before the earliest gain record a command may sit and
// still be correlated. gain records are written when a command finishes, so a
// captured command's transcript timestamp (when it was issued) can precede its
// own gain record by the command's duration. Only commands older than this grace
// are treated as predating the ledger; the window is small against the hours or
// days of pre-ledger history it is meant to exclude.
const ledgerGrace = time.Hour

// Options controls a discover scan. Transcript locations are explicit so tests
// can inject fixtures; the cmd layer populates them from the environment.
type Options struct {
	Since      time.Time
	TopN       int      // cap escaped rows shown; 0 = no cap
	ClaudeDirs []string // each a Claude config dir that contains a projects/ subdir
	CodexDir   string   // a Codex home that contains a sessions/ subdir
	// Project is the absolute project root to scope the scan to. When set, only
	// Claude sessions under projects/<slug-of-Project>/ and Codex commands whose
	// workdir is within Project are considered. Empty scans every project, which
	// can be large. This mirrors RTK's project-scoped session discovery.
	Project string
}

// EscapedRow aggregates identical escaped commands (scrubbed) across runs.
type EscapedRow struct {
	Command string // scrubbed, display-safe
	Count   int
	Agents  []string // sorted, deduped: which agents ran it
}

// Report is the analyzed, render-ready discover result.
type Report struct {
	Total       int
	ByCategory  map[Category]int
	Escaped     []EscapedRow // sorted by Count desc, then command
	ClaudeFiles int
	CodexFiles  int
	Scanned     []string // agents actually scanned ("claude", "codex")
	// LedgerStart/LedgerEnd bound the gain records considered (the correlation
	// window). Zero when the gain ledger is empty. Commands before LedgerStart are
	// counted under CatPredatesLedger, not CatEscaped.
	LedgerStart time.Time
	LedgerEnd   time.Time
}

// exec is one executed command extracted from a transcript.
type exec struct {
	agent   string
	command string // raw, as the agent emitted it
	ts      time.Time
}

// Analyze scans the configured transcripts and cross-references the gain log.
func Analyze(reg *filter.Registry, opts Options) (*Report, error) {
	rep := &Report{ByCategory: map[Category]int{}}

	var execs []exec
	claudeFiles, claudeCmds := readClaude(opts.ClaudeDirs, opts.Project, opts.Since)
	rep.ClaudeFiles = claudeFiles
	if claudeFiles > 0 {
		rep.Scanned = append(rep.Scanned, "claude")
	}
	execs = append(execs, claudeCmds...)

	codexFiles, codexCmds := readCodex(opts.CodexDir, opts.Project, opts.Since)
	rep.CodexFiles = codexFiles
	if codexFiles > 0 {
		rep.Scanned = append(rep.Scanned, "codex")
	}
	execs = append(execs, codexCmds...)

	// Build a multiset of scrubbed gain commands. gain commands are already
	// scrubbed at record time. RecentEntries with a large n returns all entries.
	entries, err := gain.RecentEntries(1 << 30)
	if err != nil {
		return nil, err
	}
	gainIndex := map[string]int{}
	for _, e := range entries {
		ts, perr := time.Parse(time.RFC3339, e.TS)
		if !opts.Since.IsZero() && perr == nil && ts.Before(opts.Since) {
			continue
		}
		gainIndex[e.Command]++
		// Track the correlation window: the span of gain records considered. A
		// transcript command outside this window cannot be correlated.
		if perr == nil {
			if rep.LedgerStart.IsZero() || ts.Before(rep.LedgerStart) {
				rep.LedgerStart = ts
			}
			if ts.After(rep.LedgerEnd) {
				rep.LedgerEnd = ts
			}
		}
	}

	escaped := map[string]*EscapedRow{}
	for _, ex := range execs {
		// A command that ran before the earliest gain record cannot match any gain
		// record. Bucket it apart from escaped so a short ledger against a long
		// transcript history does not inflate the actionable "escaped" count.
		if !rep.LedgerStart.IsZero() && !ex.ts.IsZero() && ex.ts.Before(rep.LedgerStart.Add(-ledgerGrace)) {
			rep.Total++
			rep.ByCategory[CatPredatesLedger]++
			continue
		}
		cat := classify(reg, ex.command, gainIndex)
		rep.Total++
		rep.ByCategory[cat]++
		if cat == CatEscaped {
			key := scrub.Scrub(strings.TrimSpace(ex.command))
			row := escaped[key]
			if row == nil {
				row = &EscapedRow{Command: key}
				escaped[key] = row
			}
			row.Count++
			row.Agents = addAgent(row.Agents, ex.agent)
		}
	}

	for _, row := range escaped {
		sort.Strings(row.Agents)
		rep.Escaped = append(rep.Escaped, *row)
	}
	sort.Slice(rep.Escaped, func(i, j int) bool {
		if rep.Escaped[i].Count != rep.Escaped[j].Count {
			return rep.Escaped[i].Count > rep.Escaped[j].Count
		}
		return rep.Escaped[i].Command < rep.Escaped[j].Command
	})
	return rep, nil
}

// classify decides the category of one raw command, consuming a matching gain
// record from gainIndex when a filterable segment matches. It reuses
// explain.Command so the expected handling can never drift from runtime.
func classify(reg *filter.Registry, raw string, gainIndex map[string]int) Category {
	raw = gain.StripRunPrefix(raw)
	if strings.TrimSpace(raw) == "" {
		return CatUnknown
	}
	rep := explain.Command(reg, raw)
	if len(rep.Segments) == 0 {
		return CatUnknown
	}
	wrappedAny := false
	filterable := 0
	matched := 0
	bypass := false
	for _, s := range rep.Segments {
		if !s.Wrapped {
			continue
		}
		wrappedAny = true
		switch s.RunnerMode {
		case explain.ModeFiltered, explain.ModeLive:
			filterable++
			// Match on the INNER command ctx-wire run would receive, canonicalized
			// exactly as gain stored it (argv tokenized, scrubbed, secret-redacted).
			// Keying on the raw segment text would never match a quoted or
			// pipelined command, since gain records the de-quoted last-stage argv.
			key := scrub.CommandLine(s.Inner)
			if gainIndex[key] > 0 {
				gainIndex[key]--
				matched++
			}
		case explain.ModeBypass:
			bypass = true
		}
	}
	switch {
	case !wrappedAny:
		return CatPassthrough
	case filterable == 0 && bypass:
		return CatHookLimited
	case filterable == 0:
		return CatPassthrough
	case matched == filterable:
		return CatCaptured
	default:
		// One or more filterable segments had no gain record: conservatively the
		// command escaped ctx-wire (in whole or in part).
		return CatEscaped
	}
}

func addAgent(agents []string, a string) []string {
	for _, x := range agents {
		if x == a {
			return agents
		}
	}
	return append(agents, a)
}

// ---------------------------------------------------------------------------
// Claude transcripts
// ---------------------------------------------------------------------------

type claudeLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Command string `json:"command"`
			} `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

// readClaude returns the number of transcript files scanned and the Bash
// commands extracted from them, filtered to >= since. When project is set, only
// the projects/<slug> subdir for that project is scanned (fast and relevant);
// otherwise every project's sessions are walked.
func readClaude(dirs []string, project string, since time.Time) (int, []exec) {
	var out []exec
	files := 0
	for _, base := range dirs {
		root := filepath.Join(base, "projects")
		if project != "" {
			root = filepath.Join(root, encodeClaudeProjectSlug(project))
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			files++
			out = append(out, parseClaudeFile(path, since)...)
			return nil
		})
	}
	return files, out
}

// EncodeClaudeProjectSlug reproduces Claude Code's project-directory slug so
// other packages (e.g. learn) locate the same session directory. It delegates to
// the internal encoder, keeping one source of truth for the convention.
func EncodeClaudeProjectSlug(path string) string {
	return encodeClaudeProjectSlug(path)
}

// encodeClaudeProjectSlug delegates to the canonical encoder in
// internal/transcript so the slug convention has one source of truth.
func encodeClaudeProjectSlug(path string) string {
	return transcript.EncodeClaudeProjectSlug(path)
}

func parseClaudeFile(path string, since time.Time) []exec {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []exec
	// Read line by line with bufio.Reader (not Scanner): transcript lines can be
	// many MB (large tool results), and a malformed line must be skipped without
	// dropping the rest of the file.
	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadString('\n')
		out = appendClaudeLine(out, line, since)
		if rerr != nil {
			break
		}
	}
	return out
}

func appendClaudeLine(out []exec, line string, since time.Time) []exec {
	line = strings.TrimSpace(line)
	if line == "" {
		return out
	}
	var l claudeLine
	if json.Unmarshal([]byte(line), &l) != nil || l.Type != "assistant" {
		return out // skip malformed or non-assistant lines, keep scanning
	}
	ts := parseTS(l.Timestamp)
	if !since.IsZero() && !ts.IsZero() && ts.Before(since) {
		return out
	}
	for _, c := range l.Message.Content {
		if c.Type == "tool_use" && c.Name == "Bash" && strings.TrimSpace(c.Input.Command) != "" {
			out = append(out, exec{agent: "claude", command: c.Input.Command, ts: ts})
		}
	}
	return out
}

// FileToolStat counts one transcript's built-in file-tool activity: Read and
// Grep tool uses (which bypass ctx-wire entirely) and Edit refusals caused by
// the harness's read-before-edit gate. It is the measurement side of the
// file-tools capture experiment: redirected traffic shows up as these counts
// falling while shell adoption rises.
type FileToolStat struct {
	Reads        int
	Greps        int
	EditRefusals int
	// Captures is how many Read/Grep tool calls the file-tools capture redirected
	// to a filtered shell command (a deny carrying captureMarker). It is a count
	// of redirects, not a token estimate: the tokens actually saved show up in
	// `ctx-wire gain` as the substituted nl/rg runs.
	Captures int
}

// claudeToolLine is the lean decode for file-tool counting. tool_result
// content stays raw because Claude emits it as either a string or an array of
// text blocks; a substring probe works for both shapes.
type claudeToolLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content []struct {
			Type    string          `json:"type"`
			Name    string          `json:"name"`
			Content json.RawMessage `json:"content"`
			// IsError distinguishes a real error tool_result (a deny or refusal)
			// from a successful one that merely ECHOES a marker string (e.g. a
			// Bash run that cats claude_filetools.go or prints the deny JSON). A
			// substring probe alone over-counts badly when dogfooding ctx-wire on
			// itself, so the deny/refusal cases also require IsError.
			IsError bool `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
}

// editRefusalMarker is the harness's read-before-edit refusal. Substring, not
// equality: the surrounding wording varies across Claude Code versions.
const editRefusalMarker = "has not been read yet"

// captureMarker is the lead-in of the file-tools capture deny reason. It IS the
// hook's exported constant (single source of truth), so the parser and the hook
// cannot drift apart and silently zero the metric.
const captureMarker = hook.CaptureDenyPrefix

// parseClaudeFileTools counts Read/Grep tool uses (assistant lines) and
// read-before-edit refusals (user-line tool results) in one transcript. It is
// a separate pass from parseClaudeFile because tool results live on user
// lines, which the command parser deliberately skips.
func parseClaudeFileTools(path string, since time.Time) FileToolStat {
	f, err := os.Open(path)
	if err != nil {
		return FileToolStat{}
	}
	defer f.Close()
	var st FileToolStat
	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadString('\n')
		countClaudeToolLine(&st, line, since)
		if rerr != nil {
			break
		}
	}
	return st
}

func countClaudeToolLine(st *FileToolStat, line string, since time.Time) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var l claudeToolLine
	if json.Unmarshal([]byte(line), &l) != nil {
		return
	}
	if l.Type != "assistant" && l.Type != "user" {
		return
	}
	ts := parseTS(l.Timestamp)
	if !since.IsZero() && !ts.IsZero() && ts.Before(since) {
		return
	}
	for _, c := range l.Message.Content {
		switch {
		case l.Type == "assistant" && c.Type == "tool_use" && c.Name == "Read":
			st.Reads++
		case l.Type == "assistant" && c.Type == "tool_use" && c.Name == "Grep":
			st.Greps++
		case l.Type == "user" && c.Type == "tool_result" && c.IsError && strings.Contains(string(c.Content), editRefusalMarker):
			st.EditRefusals++
		case l.Type == "user" && c.Type == "tool_result" && c.IsError && strings.Contains(string(c.Content), captureMarker):
			st.Captures++
		}
	}
}

// ---------------------------------------------------------------------------
// Codex transcripts
// ---------------------------------------------------------------------------

type codexLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"payload"`
}

func readCodex(codexHome, project string, since time.Time) (int, []exec) {
	if codexHome == "" {
		return 0, nil
	}
	sessions := filepath.Join(codexHome, "sessions")
	var out []exec
	files := 0
	_ = filepath.WalkDir(sessions, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		// Bound the scan by file modtime when a window is set, so we do not read
		// years of rollouts to honor --since.
		if !since.IsZero() {
			if info, ierr := d.Info(); ierr == nil && info.ModTime().Before(since) {
				return nil
			}
		}
		files++
		out = append(out, parseCodexFile(path, project, since)...)
		return nil
	})
	return files, out
}

func parseCodexFile(path, project string, since time.Time) []exec {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []exec
	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadString('\n')
		out = appendCodexLine(out, line, project, since)
		if rerr != nil {
			break
		}
	}
	return out
}

func appendCodexLine(out []exec, line, project string, since time.Time) []exec {
	line = strings.TrimSpace(line)
	if line == "" {
		return out
	}
	var l codexLine
	if json.Unmarshal([]byte(line), &l) != nil {
		return out // skip malformed, keep scanning
	}
	if l.Type != "response_item" || l.Payload.Type != "function_call" {
		return out
	}
	if l.Payload.Name != "exec_command" && l.Payload.Name != "shell" {
		return out
	}
	cmd, workdir := codexCommand(l.Payload.Arguments)
	if strings.TrimSpace(cmd) == "" {
		return out
	}
	if project != "" && workdir != "" && !withinProject(workdir, project) {
		return out
	}
	ts := parseTS(l.Timestamp)
	if !since.IsZero() && !ts.IsZero() && ts.Before(since) {
		return out
	}
	return append(out, exec{agent: "codex", command: cmd, ts: ts})
}

// withinProject reports whether workdir is project or nested under it.
func withinProject(workdir, project string) bool {
	workdir = strings.TrimRight(workdir, "/")
	project = strings.TrimRight(project, "/")
	return workdir == project || strings.HasPrefix(workdir, project+"/")
}

// codexCommand extracts the command and workdir from a Codex exec function-call
// arguments JSON. It handles the "cmd" string form ({"cmd":"git status",
// "workdir":"/p"}) and the "command" argv form
// ({"command":["bash","-lc","git status"]}).
func codexCommand(args string) (cmd, workdir string) {
	if args == "" {
		return "", ""
	}
	var m struct {
		Cmd     string          `json:"cmd"`
		Workdir string          `json:"workdir"`
		Cwd     string          `json:"cwd"`
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return "", ""
	}
	workdir = m.Workdir
	if workdir == "" {
		workdir = m.Cwd
	}
	if strings.TrimSpace(m.Cmd) != "" {
		return m.Cmd, workdir
	}
	if len(m.Command) > 0 {
		var argv []string
		if json.Unmarshal(m.Command, &argv) == nil {
			return joinShellArgv(argv), workdir
		}
		var s string
		if json.Unmarshal(m.Command, &s) == nil {
			return s, workdir
		}
	}
	return "", workdir
}

// joinShellArgv collapses a shell argv like ["bash","-lc","git status"] to the
// inner script, or joins a plain argv with spaces.
func joinShellArgv(argv []string) string {
	if len(argv) >= 3 {
		if base := filepath.Base(argv[0]); base == "bash" || base == "sh" || base == "zsh" {
			if argv[1] == "-lc" || argv[1] == "-c" || argv[1] == "-lic" {
				return argv[2]
			}
		}
	}
	return strings.Join(argv, " ")
}

func parseTS(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
