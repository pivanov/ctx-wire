package rewrite

import (
	"path/filepath"
	"strings"
)

// SegmentExplain describes the hook-rewrite decision for one command segment.
type SegmentExplain struct {
	Command   string `json:"command"`             // the trimmed segment text
	Wrapped   bool   `json:"wrapped"`             // true if the hook would wrap it in `ctx-wire run`
	Program   string `json:"program,omitempty"`   // executable ctx-wire receives, without a path
	Reason    string `json:"reason,omitempty"`    // when not wrapped, why (a Reason* constant)
	Inner     string `json:"inner,omitempty"`     // the command `ctx-wire run` receives (for time: the timed command)
	Rewritten string `json:"rewritten,omitempty"` // the full rewritten segment (when Wrapped)
}

// LineExplain is the full hook-rewrite explanation for a command line.
type LineExplain struct {
	Original string           `json:"original"`
	Result   string           `json:"result"` // the rewritten line (equals Original when nothing is wrapped)
	Changed  bool             `json:"changed"`
	Segments []SegmentExplain `json:"segments"`
}

// Explain reports, segment by segment, whether the hook would rewrite a command
// line and why. It reuses the same passReason logic the rewriter uses, so the
// explanation can never drift from the actual rewrite behavior.
func Explain(line string) LineExplain {
	return explainWith(line, prefix, false)
}

// RewriteMetadata reports the exact runtime rewrite plus machine-readable
// segment metadata. Unlike Explain, it includes the executable lookup gate.
func RewriteMetadata(line, agentName string) LineExplain {
	return explainWith(line, wrapForAgent(agentName), true)
}

func explainWith(line, wrap string, attestExecutable bool) LineExplain {
	result := lineWith(line, wrap)
	le := LineExplain{Original: line, Result: result, Changed: result != line}
	// A line with an unattestable construct is passed through whole (lineWith
	// refuses to rewrite it), so report it as one passthrough rather than per
	// segment, keeping Explain consistent with the actual rewrite.
	if ContainsUnattestableConstruct(line) {
		le.Segments = append(le.Segments, SegmentExplain{
			Command: strings.TrimSpace(line),
			Reason:  ReasonUnattestable,
		})
		return le
	}
	segs, _ := splitTopLevel(line)
	for _, seg := range segs {
		core := strings.TrimSpace(seg)
		if core == "" {
			continue
		}
		rewritten, inner, wrapped, reason := rewriteCore(core, wrap)
		if attestExecutable && wrapped && !lookPath(firstToken(inner)) {
			wrapped, reason = false, ReasonExecutableNotFound
		}
		if !wrapped {
			rewritten, inner = "", ""
		}
		le.Segments = append(le.Segments, SegmentExplain{
			Command:   core,
			Wrapped:   wrapped,
			Program:   programName(inner),
			Reason:    reason,
			Inner:     inner,
			Rewritten: rewritten,
		})
	}
	if len(le.Segments) == 0 {
		le.Segments = append(le.Segments, SegmentExplain{Reason: ReasonEmpty})
	}
	return le
}

func programName(inner string) string {
	token := firstToken(inner)
	if token == "" {
		return ""
	}
	if unquoted, _, ok := shellUnquoteToken(token); ok {
		token = unquoted
	}
	return filepath.Base(token)
}
