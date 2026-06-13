package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"ctx-wire/internal/rewrite"
)

// The file-tools capture experiment: when enabled, the Claude PreToolUse hook
// denies built-in Read/Grep calls it can translate EXACTLY into an equivalent
// shell command, so the traffic flows through ctx-wire's filters instead of
// bypassing them. Spec: .docs/2026-06-10-file-tools-capture-design.md.
//
// Two rules are load-bearing:
//   - Fail open everywhere: any parameter, file type, or shape we cannot map
//     faithfully allows the built-in tool. A wrong suggestion is worse than a
//     missed capture.
//   - Never deny without recorded state (denystate.go): the loop-breaker must
//     be able to see every deny, or a retrying agent could thrash.

// captureFileTools gates the experiment ([hooks] capture_file_tools, wired by
// main at startup). Default off.
var captureFileTools bool

// SetCaptureFileTools enables or disables the file-tools capture experiment.
func SetCaptureFileTools(v bool) { captureFileTools = v }

// readCapThreshold is the minimum file size for a Read deny. Small files are
// cheap and usually edit-bound (Edit requires a prior Read-tool read, which a
// shell read does not satisfy), so only large exploration reads redirect.
const readCapThreshold = 16 * 1024

// CaptureDenyPrefix is the stable lead-in of the capture deny reason. Exported
// so the transcript parser (internal/discover) can detect a real capture by the
// exact string the hook emits; a sync test pins the two together so the metric
// cannot silently zero if this wording ever changes.
const CaptureDenyPrefix = "Token savings: run "

func claudeFileTool(in claudeInput, w io.Writer) error {
	if !captureFileTools {
		return nil
	}
	suggestion, ok := mapFileToolSuggestion(in.ToolName, in.ToolInput, os.Lstat)
	if !ok {
		return nil
	}
	if !recordDenyOnce(in.SessionID, in.ToolName, in.ToolInput) {
		return nil // recent deny (agent retrying) or unrecordable state: allow
	}
	reason := fmt.Sprintf(
		CaptureDenyPrefix+"`%s` in Bash instead (the output is filtered, capped, and secrets-scrubbed by ctx-wire; the built-in tool bypasses that).",
		suggestion)
	return json.NewEncoder(w).Encode(claudeOutput{
		HookSpecificOutput: claudeHookOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
		},
	})
}

// mapFileToolSuggestion translates a Read/Grep tool_input into the exact
// equivalent shell command. ok is false for anything that cannot be mapped
// faithfully. lstat is injectable for tests.
func mapFileToolSuggestion(tool string, input json.RawMessage, lstat func(string) (os.FileInfo, error)) (string, bool) {
	switch tool {
	case "Read":
		return mapReadSuggestion(input, lstat)
	case "Grep":
		return mapGrepSuggestion(input)
	default:
		return "", false
	}
}

type claudeReadInput struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

// decodeStrictFileTool unmarshals a tool_input into v, rejecting any field the
// mapper does not know about. An unknown field means Claude is expressing
// semantics this mapping cannot translate (schema drift, a new option like
// Grep "literal"), and the load-bearing rule is that anything not exactly
// translatable fails OPEN: reject the decode, the built-in tool runs. New
// fields are only ever added to the typed structs together with exact-mapping
// tests.
func decodeStrictFileTool(raw json.RawMessage, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		return false
	}
	// Exactly one JSON value: a second decode must hit EOF, anything else is
	// trailing data this mapper has no business interpreting.
	return dec.Decode(&struct{}{}) == io.EOF
}

// readTextSuffixes is the allowlist of clearly-text extensions a Read deny may
// fire for. Everything else (images, notebooks, binaries, extensionless) is
// allowed through to the built-in tool.
var readTextSuffixes = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".rs": true, ".java": true, ".rb": true, ".php": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true,
	".md": true, ".txt": true, ".log": true, ".csv": true,
	".json": true, ".toml": true, ".yaml": true, ".yml": true, ".xml": true,
	".css": true, ".scss": true, ".html": true, ".sh": true, ".bash": true,
	".zsh": true, ".sql": true, ".proto": true, ".lock": true, ".tf": true,
}

// mapReadSuggestion redirects only unranged reads of large regular text files
// (the expensive exploration reads): ranged reads are already capped, small
// files are cheap and usually edit-bound, and anything but a plain regular
// file (dirs, symlinks, devices, stat errors) allows. The suggestion keeps
// line numbers (Read returns cat -n format) via nl -ba.
func mapReadSuggestion(raw json.RawMessage, lstat func(string) (os.FileInfo, error)) (string, bool) {
	var in claudeReadInput
	if !decodeStrictFileTool(raw, &in) {
		return "", false
	}
	if in.FilePath == "" || in.Offset != 0 || in.Limit != 0 {
		return "", false
	}
	// Absolute paths only: Claude's Read sends absolute paths, and absolute
	// paths can never start with "-" (nl has no portable "--" terminator).
	if !filepath.IsAbs(in.FilePath) {
		return "", false
	}
	if !readTextSuffixes[strings.ToLower(filepath.Ext(in.FilePath))] {
		return "", false
	}
	fi, err := lstat(in.FilePath)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() <= readCapThreshold {
		return "", false
	}
	return "nl -ba " + rewrite.ShellSingleQuote(in.FilePath), true
}

type claudeGrepInput struct {
	Pattern     string `json:"pattern"`
	Path        string `json:"path"`
	Glob        string `json:"glob"`
	Type        string `json:"type"`
	OutputMode  string `json:"output_mode"`
	CaseIns     bool   `json:"-i"`
	LineNums    bool   `json:"-n"`
	Before      int    `json:"-B"`
	After       int    `json:"-A"`
	Context     int    `json:"-C"`
	ContextLong int    `json:"context"` // newer Claude Code emits "context" instead of "-C"
	HeadLimit   int    `json:"head_limit"`
	Multiline   bool   `json:"multiline"`
}

// rgTypeRe matches the identifiers rg -t accepts; anything else fails open
// rather than risk a flag injection through the type field.
var rgTypeRe = regexp.MustCompile(`^[A-Za-z0-9+_-]+$`)

// mapGrepSuggestion translates a Grep call into the equivalent rg invocation,
// branched by output mode (the default mode is files_with_matches). The only
// deliberate inexactness is additive: content mode always passes -n, so the
// agent gets line numbers it can act on.
func mapGrepSuggestion(raw json.RawMessage) (string, bool) {
	var in claudeGrepInput
	if !decodeStrictFileTool(raw, &in) {
		return "", false
	}
	if in.Pattern == "" || in.Multiline || in.HeadLimit != 0 {
		return "", false
	}
	mode := in.OutputMode
	if mode == "" {
		mode = "files_with_matches"
	}
	var modeFlag string
	switch mode {
	case "content":
		modeFlag = "-n"
	case "files_with_matches":
		modeFlag = "-l"
	case "count":
		modeFlag = "-c"
	default:
		return "", false
	}
	ctx := in.Context
	if in.ContextLong != 0 {
		ctx = in.ContextLong
	}
	hasContext := in.Before != 0 || in.After != 0 || ctx != 0
	if hasContext && mode != "content" {
		return "", false // context flags only have defined meaning for content
	}
	if in.Before < 0 || in.After < 0 || ctx < 0 {
		return "", false
	}
	parts := []string{"rg", modeFlag}
	if in.CaseIns {
		parts = append(parts, "-i")
	}
	if in.Before > 0 {
		parts = append(parts, "-B", strconv.Itoa(in.Before))
	}
	if in.After > 0 {
		parts = append(parts, "-A", strconv.Itoa(in.After))
	}
	if ctx > 0 {
		parts = append(parts, "-C", strconv.Itoa(ctx))
	}
	if in.Type != "" {
		if !rgTypeRe.MatchString(in.Type) {
			return "", false
		}
		parts = append(parts, "-t", in.Type)
	}
	if in.Glob != "" {
		parts = append(parts, "-g", rewrite.ShellSingleQuote(in.Glob))
	}
	parts = append(parts, "--", rewrite.ShellSingleQuote(in.Pattern))
	if in.Path != "" {
		parts = append(parts, rewrite.ShellSingleQuote(in.Path))
	}
	return strings.Join(parts, " "), true
}
