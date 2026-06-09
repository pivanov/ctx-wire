// Package mcpcompress reduces verbose MCP tool-result text (chrome-devtools and
// Playwright accessibility snapshots) before it reaches the agent, while preserving
// the structure the agent acts on. It is deliberately SUBTRACTIVE: it only ever
// drops whole lines (page-chrome subtrees and redundant text), and never rewrites,
// renumbers, or regenerates a line it keeps. That invariant is the safety spine:
// a dropped element is merely invisible to the agent (it never tries to use it),
// but a *renumbered* ref (chrome-devtools uid / Playwright [ref=eN]) would hand the
// agent a valid-looking reference pointing at the WRONG element, a silent
// wrong-click. The full raw result is always recoverable upstream, so stripping is
// safe to be conservative.
package mcpcompress

import "strings"

// ReduceSnapshot reduces an accessibility-snapshot result, dispatching on the
// dialect: Playwright's YAML-ish `[ref=eN]` tree vs chrome-devtools' `uid=` lines.
// Both reducers self-gate to a no-op (s, 0) on text that is not a snapshot, so it
// is safe to call on any tool result.
func ReduceSnapshot(s string) (string, int) {
	if strings.Contains(s, "[ref=e") {
		return ReducePlaywrightSnapshot(s)
	}
	return ReduceSnapshotText(s)
}

// chromeRoles are ARIA landmark roles that are page chrome (site header / footer):
// the same on nearly every page, rarely the agent's actual target. Their whole
// subtree is dropped. The raw snapshot remains recoverable if the agent needs them.
var chromeRoles = map[string]bool{
	"banner":      true, // page header: logo, global nav, sign-in
	"contentinfo": true, // page footer
}

// ReduceSnapshotText reduces a chrome-devtools accessibility-snapshot text. It
// drops chrome-landmark subtrees and StaticText nodes that merely repeat their
// parent's accessible name, keeping every other line BYTE-IDENTICAL (uids
// untouched). It never renumbers or regenerates. Returns the reduced text and the
// number of lines dropped; a return of (s, 0) means nothing matched (safe no-op).
func ReduceSnapshotText(s string) (string, int) {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	dropped := 0
	parentName := map[int]string{} // indent -> accessible name of the kept node there
	skipBelow := -1                // while >=0, drop every line deeper than this indent
	for _, ln := range lines {
		indent := countIndent(ln)
		if skipBelow >= 0 {
			if indent > skipBelow {
				dropped++
				continue
			}
			skipBelow = -1 // back to the dropped node's level or shallower
		}
		role, name, ok := parseNode(ln)
		if !ok {
			out = append(out, ln) // header ("## ...") or non-node line: keep verbatim
			continue
		}
		if chromeRoles[role] {
			skipBelow = indent // drop this node's subtree
			dropped++          // and the node line itself
			continue
		}
		if role == "StaticText" && name != "" && name == parentName[indent-2] {
			dropped++ // redundant: just repeats the parent's accessible name
			continue
		}
		parentName[indent] = name
		out = append(out, ln)
	}
	if dropped == 0 {
		return s, 0
	}
	return strings.Join(out, "\n"), dropped
}

func countIndent(ln string) int {
	n := 0
	for n < len(ln) && ln[n] == ' ' {
		n++
	}
	return n
}

// parseNode extracts the role and first quoted accessible name from a snapshot
// line of the form `  uid=1_7 button "Platform" expandable`. ok is false for a
// line that is not a `uid=` node (e.g. the "## Latest page snapshot" header),
// which the caller keeps verbatim.
func parseNode(ln string) (role, name string, ok bool) {
	t := strings.TrimLeft(ln, " ")
	if !strings.HasPrefix(t, "uid=") {
		return "", "", false
	}
	sp := strings.IndexByte(t, ' ')
	if sp < 0 {
		return "", "", false // a bare "uid=..." with no role; leave it to the caller
	}
	rest := t[sp+1:]
	if rsp := strings.IndexByte(rest, ' '); rsp >= 0 {
		role = rest[:rsp]
	} else {
		role = rest
	}
	if q := strings.IndexByte(rest, '"'); q >= 0 {
		if q2 := strings.IndexByte(rest[q+1:], '"'); q2 >= 0 {
			name = rest[q+1 : q+1+q2]
		}
	}
	return role, name, true
}

// ReducePlaywrightSnapshot reduces a Playwright `browser_snapshot` accessibility
// tree (YAML-ish lines like `  - link "Home" [ref=e10] [cursor=pointer]:`). Like
// the chrome-devtools reducer it is SUBTRACTIVE: it drops banner/contentinfo
// subtrees, `- text:` nodes that repeat their parent's name, and NAMELESS generic
// LEAF nodes (empty containers the agent never targets), keeping every other line
// BYTE-IDENTICAL so no `[ref=eN]` is ever renumbered. A nameless generic WRAPPER
// (with children) is kept, because collapsing it would re-indent its children and
// break byte-identity. Returns (s, 0) when nothing matched.
func ReducePlaywrightSnapshot(s string) (string, int) {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	dropped := 0
	parentName := map[int]string{} // indent -> name of the kept node there
	skipBelow := -1                // while >=0, drop every line deeper than this indent
	for _, ln := range lines {
		indent := countIndent(ln)
		if skipBelow >= 0 {
			if indent > skipBelow {
				dropped++
				continue
			}
			skipBelow = -1
		}
		role, name, ok := pwParse(ln)
		if !ok {
			out = append(out, ln) // fence, "### Page" header, "- /url:" property: keep verbatim
			continue
		}
		if chromeRoles[role] {
			skipBelow = indent // drop the banner/contentinfo subtree
			dropped++
			continue
		}
		if role == "text" {
			if v := pwTextValue(ln); v != "" && v == parentName[indent-2] {
				dropped++ // redundant text repeating the parent's accessible name
				continue
			}
		}
		if role == "generic" && name == "" && pwIsLeaf(ln) {
			dropped++ // a nameless empty container is never an action target
			continue
		}
		parentName[indent] = name
		out = append(out, ln)
	}
	if dropped == 0 {
		return s, 0
	}
	return strings.Join(out, "\n"), dropped
}

// pwParse extracts the role and first quoted name from a Playwright node line.
// ok is false for a non-node line (a fence, a `### ...` header, or a property like
// `- /url: ...` whose first token is not a letter), which the caller keeps verbatim.
func pwParse(ln string) (role, name string, ok bool) {
	t := strings.TrimLeft(ln, " ")
	if !strings.HasPrefix(t, "- ") {
		return "", "", false
	}
	rest := t[2:]
	n := 0
	for n < len(rest) && isLetter(rest[n]) {
		n++
	}
	if n == 0 {
		return "", "", false // e.g. "- /url:" property line
	}
	role = rest[:n]
	if q := strings.IndexByte(rest, '"'); q >= 0 {
		if q2 := strings.IndexByte(rest[q+1:], '"'); q2 >= 0 {
			name = rest[q+1 : q+1+q2]
		}
	}
	return role, name, true
}

// pwIsLeaf reports whether a Playwright node line has no children. A node with
// children ends in ":"; a leaf does not.
func pwIsLeaf(ln string) bool {
	return !strings.HasSuffix(strings.TrimRight(ln, " "), ":")
}

// pwTextValue returns the value of a `- text: X` node (unquoted), or "" otherwise.
func pwTextValue(ln string) string {
	t := strings.TrimLeft(ln, " ")
	const p = "- text: "
	if !strings.HasPrefix(t, p) {
		return ""
	}
	return strings.Trim(strings.TrimSpace(t[len(p):]), `"`)
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
