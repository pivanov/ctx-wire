// Package permission reads an AI agent's Bash permission rules (Claude Code
// settings.json permissions.deny/ask/allow) so ctx-wire's hook does not
// auto-approve a command the user's own rules would block.
//
// The problem: ctx-wire rewrites a command into `ctx-wire run <cmd>` and tells
// the agent to allow it. The agent's deny/ask rules match the inner command,
// but they can no longer see it through the wrapper, so a blanket allow would
// silently bypass them. The fix is conservative: if a deny or ask rule matches
// the inner command, ctx-wire steps aside (emits no decision) and lets the agent
// apply its own rule to the original command. It never re-implements
// enforcement, and a missing/empty/broken settings file yields Allow (fail-open,
// preserving the transparent rewrite for normal commands).
package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Verdict is the decision for a command under the loaded rules.
type Verdict int

const (
	// Allow: no deny/ask rule matches; ctx-wire keeps its transparent rewrite.
	Allow Verdict = iota
	// Ask: an ask rule matches; ctx-wire steps aside so the agent can prompt.
	Ask
	// Deny: a deny rule matches; ctx-wire steps aside so the agent blocks it.
	Deny
)

// Rules holds the Bash permission patterns extracted from settings files.
// Only deny and ask drive the verdict; allow rules are never consulted, since
// ctx-wire's whole purpose is to keep its transparent rewrite (an implicit
// allow) unless a deny/ask rule says otherwise.
type Rules struct {
	deny []string
	ask  []string
}

type settingsFile struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
		Ask   []string `json:"ask"`
	} `json:"permissions"`
}

// Load reads settings.json and settings.local.json from each dir and merges the
// Bash rules. All rules are merged (no override); precedence is applied at
// decision time. Missing or unreadable files are skipped (fail-open).
func Load(dirs ...string) Rules {
	var r Rules
	for _, d := range dirs {
		if d == "" {
			continue
		}
		for _, name := range []string{"settings.json", "settings.local.json"} {
			r.mergeFile(filepath.Join(d, name))
		}
	}
	return r
}

// LoadClaude loads rules from the Claude config dir (CLAUDE_CONFIG_DIR, else
// ~/.claude) and the project's .claude directory (relative to cwd). It is
// best-effort: any resolution failure simply yields fewer rules.
func LoadClaude() Rules {
	var dirs []string
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		dirs = append(dirs, d)
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".claude"))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(wd, ".claude"))
	}
	return Load(dirs...)
}

func (r *Rules) mergeFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var sf settingsFile
	if json.Unmarshal(data, &sf) != nil {
		return
	}
	r.deny = append(r.deny, bashPatterns(sf.Permissions.Deny)...)
	r.ask = append(r.ask, bashPatterns(sf.Permissions.Ask)...)
}

// Decide returns the verdict for a command line. Precedence is Deny > Ask, and
// when neither matches the default is Allow (ctx-wire keeps its rewrite). A
// compound command denies/asks if ANY of its top-level segments does.
func (r Rules) Decide(command string) Verdict {
	if len(r.deny) == 0 && len(r.ask) == 0 {
		return Allow
	}
	segs := splitSegments(command)
	if matchesAny(r.deny, segs) {
		return Deny
	}
	if matchesAny(r.ask, segs) {
		return Ask
	}
	return Allow
}

func matchesAny(pats, segs []string) bool {
	for _, seg := range segs {
		for _, p := range pats {
			if matchBash(p, seg) {
				return true
			}
		}
	}
	return false
}

// bashPatterns extracts the inner pattern from Bash permission entries. A bare
// "Bash" entry means all Bash commands. Entries for other tools are ignored.
func bashPatterns(entries []string) []string {
	var out []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		switch {
		case e == "Bash":
			out = append(out, "*")
		case strings.HasPrefix(e, "Bash(") && strings.HasSuffix(e, ")"):
			out = append(out, strings.TrimSpace(e[len("Bash("):len(e)-1]))
		}
	}
	return out
}

// matchBash matches a Claude Bash rule pattern against a command segment. It
// supports the `prefix:*` form (matches the prefix and anything after a space),
// `*` globs, and exact match.
func matchBash(pattern, command string) bool {
	pattern = strings.TrimSpace(pattern)
	command = strings.TrimSpace(command)
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == ":*" {
		return true
	}
	if strings.HasSuffix(pattern, ":*") {
		p := strings.TrimSpace(strings.TrimSuffix(pattern, ":*"))
		return command == p || strings.HasPrefix(command, p+" ")
	}
	if strings.Contains(pattern, "*") {
		return globMatch(pattern, command)
	}
	return command == pattern
}

// globMatch matches a pattern containing `*` wildcards against s.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	first, last := parts[0], parts[len(parts)-1]
	if !strings.HasPrefix(s, first) {
		return false
	}
	if !strings.HasSuffix(s, last) {
		return false
	}
	// Guard against the prefix and suffix overlapping in a short string: e.g.
	// pattern "aa*aa" must not match "aaa" (prefix and suffix would share runes).
	if len(first)+len(last) > len(s) {
		return false
	}
	s = s[len(first):]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return true
}

// splitSegments splits a command line into top-level segments on &&, ||, ;, and
// |, respecting single and double quotes. It is used so a deny on any segment of
// a compound command is honored.
func splitSegments(command string) []string {
	var segs []string
	var cur strings.Builder
	var quote rune
	runes := []rune(command)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			// Inside double quotes a backslash escapes the next rune, so a \"
			// does not close the string. Single quotes take no escapes.
			if quote == '"' && r == '\\' && i+1 < len(runes) {
				cur.WriteRune(r)
				i++
				cur.WriteRune(runes[i])
				continue
			}
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			cur.WriteRune(r)
		case ';':
			flush()
		case '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
			flush()
		case '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				i++
				flush()
			} else {
				cur.WriteRune(r)
			}
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	if len(segs) == 0 {
		return []string{strings.TrimSpace(command)}
	}
	return segs
}
