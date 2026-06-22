package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/scrub"
	"ctx-wire/internal/tee"
)

// The native-Read ceiling: a PostToolUse hook for Claude's built-in Read tool.
// Unlike a PreToolUse hook (which can only allow/deny/ask), PostToolUse can
// REPLACE the tool result the model sees via hookSpecificOutput.updatedToolOutput,
// so this is the only mechanism that shrinks a native Read's output without the
// deny footgun (the native Read still runs, so Edit's read-before-edit cache stays
// valid). Wired by `ctx-wire init claude --read-ceiling[=measure]`.
//
// Modes (config [hooks] read_ceiling, env CTX_WIRE_READ_CEILING overrides):
//   - "off"     : no-op.
//   - "measure" : log the would-be reclaim (and the real tool_response shape) to
//     the spike dir, but do NOT rewrite. Use this to size the win before enabling.
//   - "on"      : scrub + ceiling large unranged reads to head+tail with a
//     recoverable `ctx-wire fetch <hash>` handle.
//
// Invariants: only large UNRANGED reads are reshaped (a ranged read is the
// agent's own bound); emitted bytes are scrubbed fail-closed (native Read bypasses
// scrubbing, so this is a net gain); the full body is spooled scrubbed so the
// elided middle is recoverable. Fail open everywhere: any parse/shape/scrub/IO
// problem leaves the native output untouched.

const (
	// ceilMinLines is the smallest Read (in lines) the ceiling will touch.
	// Native Read already caps ~2000 lines, and the workload is small/ranged
	// dominated, so only a genuinely large read is worth reshaping.
	ceilMinLines  = 120
	ceilHeadLines = 60
	ceilTailLines = 30
)

// claudePostToolUse handles a PostToolUse payload for the read-ceiling spike.
// It only acts on Read; everything else is a no-op passthrough.
func claudePostToolUse(in claudeInput, w io.Writer) error {
	if in.ToolName != "Read" {
		return nil
	}
	mode := effectiveReadCeilingMode()
	if mode == "off" {
		return nil
	}
	// Honor explicit ranges: a ranged Read (offset/limit) is the agent's own
	// bound, so it is never ceilinged. This respects intent and keeps recovery
	// open, the fetch handle points the agent at the full body, and a ranged
	// re-read of the same file must not itself be reshaped.
	if readIsRanged(in.ToolInput) {
		return nil
	}
	path := readCeilingPath(in.ToolInput)
	rawBytes := len(in.ToolResponse)

	text, rewrap, ok := extractReadText(in.ToolResponse)
	if !ok {
		// Unknown shape: record it so we learn the real schema, then pass through.
		logSpikeSample(path, in.ToolResponse, "no-text-field")
		logSpikeMeasure(path, rawBytes, rawBytes, "no-text-field")
		return nil
	}
	if !overCeiling(text) {
		logSpikeMeasure(path, rawBytes, rawBytes, "under-threshold")
		return nil
	}

	// Scrub fail-closed: the head+tail we emit enters model context, so it must
	// honor ctx-wire's scrub guarantee (native Read bypasses scrubbing entirely;
	// this is a net gain). If scrubbing cannot be guaranteed, do NOT rewrite,
	// leave the native output (no worse than today).
	scrubbed, sok := scrub.ScrubFailClosed(text)
	if !sok {
		logSpikeMeasure(path, rawBytes, rawBytes, "scrub-failopen")
		return nil
	}

	// Recoverability: spool the FULL (scrubbed) body so the elided middle is
	// fetchable, the same contract as the shell ceiling. Spool only when we will
	// actually rewrite; measure mode uses a same-length placeholder so the
	// reported size matches. Fail open: if the spool fails, fall back to a ranged
	// re-read hint (the file is on disk).
	rewrite := mode == "on"
	recovery := "ctx-wire fetch <hash>"
	if rewrite {
		if h, ok := tee.SpoolReader("read-ceiling", strings.NewReader(scrubbed)); ok {
			recovery = "ctx-wire fetch " + h
		} else if path != "" {
			recovery = "re-Read " + path + " with an explicit offset/limit"
		}
	}

	ceil, _ := ceilingText(scrubbed, recovery)
	newResp := rewrap(ceil)
	emitted := len(newResp)
	if !rewrite {
		// Measure mode: show the reclaim we WOULD get, but do not rewrite.
		logSpikeSample(path, in.ToolResponse, "would-rewrite")
		logSpikeMeasure(path, rawBytes, emitted, "would-rewrite")
		return nil
	}

	logSpikeSample(path, in.ToolResponse, "rewrote")
	logSpikeMeasure(path, rawBytes, emitted, "rewrote")
	recordReadCeilingGain(rawBytes, emitted)
	return json.NewEncoder(w).Encode(claudePostOutput{
		HookSpecificOutput: claudePostHookOutput{
			HookEventName:     "PostToolUse",
			UpdatedToolOutput: newResp,
		},
	})
}

// recordReadCeilingGain records the reclaim in the shared gain log under a "Read"
// program key, so the read-ceiling's savings show in `ctx-wire gain` and flow to
// telemetry via the existing gain-summary rebuild (telemetry.ReportImpact ->
// totalsFromSummary, which already applies the buildImpactPayload + LastReported
// high-water-mark discipline). The gain log is O_APPEND multi-process safe, so the
// hook process can write it alongside the runner without a second telemetry writer
// (no state-file race, no new leak path). Only the actual rewrite is recorded;
// measure mode reshapes nothing, so it has no saving to claim.
func recordReadCeilingGain(rawBytes, emittedBytes int) {
	if !gain.Enabled() {
		return
	}
	_ = gain.RecordWithMeta("Read", "read-ceiling", "read-ceiling", agent.Current(), "hook", rawBytes, emittedBytes, 0)
}

// readIsRanged reports whether the Read tool_input carries an explicit
// offset/limit (the agent already bounded the read).
func readIsRanged(input json.RawMessage) bool {
	var ti struct {
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
	}
	_ = json.Unmarshal(input, &ti)
	return ti.Offset != 0 || ti.Limit != 0
}

// overCeiling reports whether a body is long enough to ceiling. It mirrors
// ceilingText's threshold exactly (ceilMinLines > ceilHeadLines+ceilTailLines,
// so exceeding the line minimum always leaves a positive elided count).
func overCeiling(text string) bool {
	return len(strings.Split(text, "\n")) > ceilMinLines
}

type claudePostOutput struct {
	HookSpecificOutput claudePostHookOutput `json:"hookSpecificOutput"`
}

type claudePostHookOutput struct {
	HookEventName     string          `json:"hookEventName"`
	UpdatedToolOutput json.RawMessage `json:"updatedToolOutput"`
}

// readCeilingMode is the configured mode ("off"/"measure"/"on"), wired by main
// from config. The env CTX_WIRE_READ_CEILING overrides it per invocation.
var readCeilingMode = "off"

// SetReadCeilingMode sets the configured read-ceiling mode.
func SetReadCeilingMode(m string) { readCeilingMode = normalizeCeilingMode(m) }

// effectiveReadCeilingMode resolves the active mode: the env override if it
// names one, else the configured mode. "off" | "measure" | "on".
func effectiveReadCeilingMode() string {
	if env := strings.TrimSpace(os.Getenv("CTX_WIRE_READ_CEILING")); env != "" {
		return normalizeCeilingMode(env)
	}
	return normalizeCeilingMode(readCeilingMode)
}

func normalizeCeilingMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "off", "no", "disable", "disabled":
		return "off"
	case "measure", "discover", "discovery", "dry-run":
		return "measure"
	default: // "" (unset), "1", "true", "on", "yes", anything else: DEFAULT ON
		return "on"
	}
}

// readTextFields is the priority list of top-level object fields that might hold
// a Read's text body, tried after the verified nested shape. Kept as a fail-open
// fallback for harness-shape drift.
var readTextFields = []string{"content", "text", "output", "stdout", "result"}

// extractReadText pulls the text body out of a Read tool_response and returns a
// rewrap closure that re-encodes a replacement body in the SAME shape (so the
// updatedToolOutput matches the tool's output schema). ok is false for any shape
// it cannot map, which fails open.
//
// The verified Claude Code Read shape (confirmed live 2026-06-22) is nested:
//
//	{"type":"text","file":{"filePath":..,"content":<text>,"numLines":..,"startLine":..,"totalLines":..}}
//
// so Shape C handles file.content and preserves every sibling key (replacing the
// content value only keeps the schema intact; numLines/totalLines stay the real
// file's counts, and the in-body marker reports what was elided).
func extractReadText(resp json.RawMessage) (text string, rewrap func(string) json.RawMessage, ok bool) {
	if len(resp) == 0 {
		return "", nil, false
	}
	// Shape A: a bare JSON string.
	var s string
	if json.Unmarshal(resp, &s) == nil {
		return s, func(n string) json.RawMessage {
			b, _ := json.Marshal(n)
			return b
		}, true
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(resp, &m) != nil {
		return "", nil, false
	}
	// Shape C (verified): {"type":"text","file":{"content":<text>,...}}.
	if fileRaw, present := m["file"]; present {
		var fm map[string]json.RawMessage
		if json.Unmarshal(fileRaw, &fm) == nil {
			if cRaw, hasContent := fm["content"]; hasContent {
				var c string
				if json.Unmarshal(cRaw, &c) == nil {
					return c, func(n string) json.RawMessage {
						nf := make(map[string]json.RawMessage, len(fm))
						for k, v := range fm {
							nf[k] = v
						}
						nb, _ := json.Marshal(n)
						nf["content"] = nb
						nfRaw, _ := json.Marshal(nf)
						nm := make(map[string]json.RawMessage, len(m))
						for k, v := range m {
							nm[k] = v
						}
						nm["file"] = nfRaw
						out, _ := json.Marshal(nm)
						return out
					}, true
				}
			}
		}
	}
	// Shape B (fallback): an object with a top-level string text field.
	for _, k := range readTextFields {
		raw, present := m[k]
		if !present {
			continue
		}
		var fs string
		if json.Unmarshal(raw, &fs) != nil {
			continue
		}
		key := k
		return fs, func(n string) json.RawMessage {
			cp := make(map[string]json.RawMessage, len(m))
			for kk, vv := range m {
				cp[kk] = vv
			}
			nb, _ := json.Marshal(n)
			cp[key] = nb
			out, _ := json.Marshal(cp)
			return out
		}, true
	}
	return "", nil, false
}

// ceilingText keeps the head and tail of a large text body verbatim and replaces
// the middle with a one-line marker that carries a recovery hint (so the elision
// is recoverable, not lossy). Returns changed=false for short bodies.
func ceilingText(text, recovery string) (string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) <= ceilMinLines {
		return text, false
	}
	elided := len(lines) - ceilHeadLines - ceilTailLines
	if elided <= 0 {
		return text, false
	}
	var b strings.Builder
	for _, l := range lines[:ceilHeadLines] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "[... ctx-wire read-ceiling: %d lines elided (kept first %d + last %d); recover full body: %s ...]\n",
		elided, ceilHeadLines, ceilTailLines, recovery)
	tail := lines[len(lines)-ceilTailLines:]
	for i, l := range tail {
		b.WriteString(l)
		if i < len(tail)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String(), true
}

// readCeilingPath best-effort extracts the file_path from a Read tool_input for
// logging; "" when absent.
func readCeilingPath(input json.RawMessage) string {
	var ti struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(input, &ti)
	return ti.FilePath
}

// spikeDir is the /tmp folder the spike writes to (override with
// CTX_WIRE_SPIKE_DIR). Defaults to a literal /tmp path (not os.TempDir(), which
// on macOS is a per-user /var/folders dir) so the data lands where expected.
// All writes here are best-effort and never block the hook.
func spikeDir() string {
	if d := strings.TrimSpace(os.Getenv("CTX_WIRE_SPIKE_DIR")); d != "" {
		return d
	}
	return "/tmp/ctx-wire-spike"
}

// spikeLogEnabled gates the /tmp instrumentation. Measure mode always logs (that
// is its purpose); on mode logs only when the user explicitly opts in by setting
// CTX_WIRE_SPIKE_DIR, so a shipped "on" feature does not clutter /tmp.
func spikeLogEnabled() bool {
	return os.Getenv("CTX_WIRE_SPIKE_DIR") != "" || effectiveReadCeilingMode() == "measure"
}

// logSpikeMeasure appends one tab-separated row of per-Read sizing to measure.tsv.
func logSpikeMeasure(path string, raw, emitted int, action string) {
	if !spikeLogEnabled() {
		return
	}
	dir := spikeDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	file := filepath.Join(dir, "measure.tsv")
	newFile := false
	if _, err := os.Stat(file); err != nil {
		newFile = true
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if newFile {
		fmt.Fprintln(f, "ts\tpath\traw_bytes\temitted_bytes\tsaved_bytes\tsaved_pct\taction")
	}
	saved := raw - emitted
	pct := 0.0
	if raw > 0 {
		pct = 100 * float64(saved) / float64(raw)
	}
	fmt.Fprintf(f, "%s\t%s\t%d\t%d\t%d\t%.1f\t%s\n",
		time.Now().UTC().Format(time.RFC3339), path, raw, emitted, saved, pct, action)
}

// logSpikeSample appends the (truncated) raw tool_response shape to samples.jsonl
// so the real Read output schema can be inspected. This is the discovery half.
func logSpikeSample(path string, resp json.RawMessage, action string) {
	if !spikeLogEnabled() {
		return
	}
	dir := spikeDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "samples.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	sample := resp
	const maxSample = 4096
	truncated := false
	if len(sample) > maxSample {
		sample = sample[:maxSample]
		truncated = true
	}
	rec := map[string]any{
		"ts":               time.Now().UTC().Format(time.RFC3339),
		"path":             path,
		"action":           action,
		"raw_bytes":        len(resp),
		"response_type":    jsonTypeOf(resp),
		"sample_truncated": truncated,
		"response_sample":  string(sample),
	}
	b, _ := json.Marshal(rec)
	f.Write(append(b, '\n'))
}

// jsonTypeOf names the top-level JSON kind of raw for the discovery log.
func jsonTypeOf(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "empty"
	}
	switch s[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	default:
		return "number"
	}
}
