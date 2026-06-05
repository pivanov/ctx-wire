package tune

import (
	"regexp"
	"strings"

	"ctx-wire/internal/scrub"
)

const (
	// defaultMaxCommandLen caps how many runes of a sanitized sample command are
	// displayed, so a pathological argv cannot dominate the preview.
	defaultMaxCommandLen = 120
	// pathCompactThreshold is the number of path segments above which an absolute
	// path is compacted. A path with this many or fewer segments is left intact.
	pathCompactThreshold = 5
	// pathKeepTail is how many trailing segments a compacted path keeps, since the
	// tail (the actual file) is the useful part for a filter author.
	pathKeepTail = 2
)

// Sanitizer applies the export-privacy rules to a scrubbed sample command before
// it is displayed or written to a bundle:
//
//   - re-runs scrub.Scrub as defense in depth (samples are already scrubbed at
//     record time, and scrubbing is idempotent),
//   - replaces the user home prefix with $HOME,
//   - replaces the current project root with $PROJECT,
//   - compacts long absolute paths while keeping the useful tail,
//   - caps the displayed command length.
//
// It carries no I/O and makes no network calls; it is a pure string transform.
type Sanitizer struct {
	Home    string // absolute user home dir; "" disables home redaction
	Project string // absolute project root; "" disables project redaction
	MaxLen  int    // rune cap for the displayed command; 0 uses the default
}

// NewSanitizer builds a Sanitizer for the given home and project roots. Roots
// are trimmed of surrounding space and trailing slashes; "/" and "" are treated
// as "no redaction" so a root of "/" can never blank out an entire command.
func NewSanitizer(home, project string) Sanitizer {
	return Sanitizer{Home: cleanRoot(home), Project: cleanRoot(project), MaxLen: defaultMaxCommandLen}
}

func cleanRoot(p string) string {
	return strings.TrimRight(strings.TrimSpace(p), "/")
}

// Sample returns the sanitized, display-ready form of a sample command.
func (s Sanitizer) Sample(sample string) string {
	out := scrub.Scrub(sample)
	out = s.redactRoots(out)
	out = compactPaths(out)
	max := s.MaxLen
	if max == 0 {
		max = defaultMaxCommandLen
	}
	return capLen(out, max)
}

// redactRoots replaces the project root first (it is usually the more specific
// path, often nested under home) and then the home root, so a project-local
// path becomes $PROJECT/... rather than $HOME/....
func (s Sanitizer) redactRoots(in string) string {
	if len(s.Project) > 1 {
		in = replaceRoot(in, s.Project, "$PROJECT")
	}
	if len(s.Home) > 1 {
		in = replaceRoot(in, s.Home, "$HOME")
	}
	return in
}

// replaceRoot replaces a filesystem root only when it appears as a complete
// path prefix. Raw substring replacement would turn /repo-other into
// $PROJ-other when the configured project root is /repo.
func replaceRoot(s, root, marker string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, root)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(root)
		if isRootStartBoundary(s, i) && isRootEndBoundary(s, end) {
			b.WriteString(s[:i])
			b.WriteString(marker)
			s = s[end:]
			continue
		}
		b.WriteString(s[:end])
		s = s[end:]
	}
}

func isRootStartBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	switch s[i-1] {
	case ' ', '\t', '\n', '\r', '\'', '"', '`', '(', '=', ':':
		return true
	default:
		return false
	}
}

func isRootEndBoundary(s string, i int) bool {
	if i == len(s) {
		return true
	}
	switch s[i] {
	case '/', ' ', '\t', '\n', '\r', '\'', '"', '`', ')':
		return true
	default:
		return false
	}
}

// pathRE matches an absolute or $HOME/$PROJECT-rooted path that starts at a
// token boundary (start, whitespace, quote, or "("). Anchoring on a boundary
// avoids mangling the path component of a URL like https://host/a/b/c, whose
// leading slash is preceded by ":".
var pathRE = regexp.MustCompile("(^|[\\s'\"`(])(\\$HOME|\\$PROJECT)?(/[^\\s'\"`)]+)")

// compactPaths compacts long path-like substrings in place, preserving the rest
// of the command (including quoting and spacing) untouched.
func compactPaths(s string) string {
	return pathRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := pathRE.FindStringSubmatch(m)
		boundary, marker, path := sub[1], sub[2], sub[3]
		return boundary + compactPath(marker+path)
	})
}

// compactPath compacts a single path (which may begin with $HOME, $PROJECT, or
// "/"). Paths at or under pathCompactThreshold segments are returned unchanged.
func compactPath(p string) string {
	marker := ""
	rest := p
	switch {
	case strings.HasPrefix(p, "$HOME"):
		marker, rest = "$HOME", p[len("$HOME"):]
	case strings.HasPrefix(p, "$PROJECT"):
		marker, rest = "$PROJECT", p[len("$PROJECT"):]
	}
	absolute := strings.HasPrefix(rest, "/")
	trimmed := strings.TrimPrefix(rest, "/")
	if trimmed == "" {
		return p
	}
	segs := strings.Split(trimmed, "/")
	if len(segs) <= pathCompactThreshold {
		return p
	}
	tail := strings.Join(segs[len(segs)-pathKeepTail:], "/")
	if marker != "" {
		// The marker already stands in for the head, so keep only the tail.
		return marker + "/.../" + tail
	}
	out := segs[0] + "/.../" + tail
	if absolute {
		out = "/" + out
	}
	return out
}

// capLen truncates s to at most max runes, marking truncation with "...".
func capLen(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}
