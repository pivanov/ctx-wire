package install

// Codex agent-attribution env injection.
//
// This file is a DELIBERATE exception to the prior "ctx-wire never writes
// ~/.codex/config.toml" stance (see CodexHooksEnabled): [features] hooks = true
// stays user-owned because it changes Codex's execution behavior, while
// shell_environment_policy.set.CTX_WIRE_AGENT = "codex" is additive telemetry
// attribution only. It labels gain entries when Codex's sandbox blocks the `ps`
// process-tree walk (fork/exec /bin/ps: operation not permitted), which
// otherwise leaves direct `ctx-wire run` commands unattributed. It grants Codex
// no new execution power or trust, and `ctx-wire uninstall` reverts exactly
// this key.
//
// Editing is SURGICAL, never decode/re-encode: the file is user-owned and a
// round-trip would churn comments and formatting. The TOML decoder is used
// only to detect state; writes are line edits on shapes the scanner positively
// recognizes. Anything ambiguous fails open to CodexEnvManual and the caller
// prints the exact snippet instead of writing. Known limitation: Codex
// profiles ([profiles.X]) may override the top-level shell_environment_policy,
// so runtime proof of attribution still comes from later gain entries with
// agent=codex, not from this file alone.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// CodexAgentEnvKey/Value are the exact pair ctx-wire owns in
// shell_environment_policy.set.
const (
	CodexAgentEnvKey   = "CTX_WIRE_AGENT"
	CodexAgentEnvValue = "codex"
)

// CodexAgentEnvSnippet is the manual fallback printed when the config shape
// cannot be confidently edited.
const CodexAgentEnvSnippet = "[shell_environment_policy]\nset = { " + CodexAgentEnvKey + " = \"" + CodexAgentEnvValue + "\" }"

// CodexEnvResult describes the outcome of an agent-env install or uninstall.
type CodexEnvResult int

const (
	// CodexEnvUpdated means the file was written (key added or removed).
	CodexEnvUpdated CodexEnvResult = iota
	// CodexEnvNoChange means the file was already in the desired state.
	CodexEnvNoChange
	// CodexEnvUserManaged means the user set CTX_WIRE_AGENT to a different
	// value; ctx-wire preserves that choice in both directions.
	CodexEnvUserManaged
	// CodexEnvManual means the config shape was not confidently editable; the
	// caller should print CodexAgentEnvSnippet instead.
	CodexEnvManual
)

var (
	codexPolicyHeaderRe    = regexp.MustCompile(`^\[shell_environment_policy\]\s*(#.*)?$`)
	codexPolicySetHeaderRe = regexp.MustCompile(`^\[shell_environment_policy\.set\]\s*(#.*)?$`)
	// codexSetLineRe captures an inline set table: indent, "set = {", inner,
	// close + optional trailing comment. Greedy inner is safe because
	// codexSetPairsRe then rejects any inner containing braces or '#'.
	codexSetLineRe = regexp.MustCompile(`^(\s*)set(\s*=\s*)\{(.*)\}(\s*(?:#.*)?)$`)
	// codexSetPairsRe positively recognizes the only inline contents we edit:
	// empty, or simple KEY = "value" pairs with no escapes, braces, or comments.
	codexSetPairsRe   = regexp.MustCompile(`^\s*$|^\s*[A-Za-z0-9_-]+\s*=\s*"[^"{}#]*"(\s*,\s*[A-Za-z0-9_-]+\s*=\s*"[^"{}#]*")*\s*$`)
	codexOurPairRe    = regexp.MustCompile(`^` + CodexAgentEnvKey + `\s*=\s*"` + CodexAgentEnvValue + `"$`)
	codexOurKeyLineRe = regexp.MustCompile(`^` + CodexAgentEnvKey + `\s*=\s*"` + CodexAgentEnvValue + `"\s*(#.*)?$`)
)

// codexAgentEnvValue reads shell_environment_policy.set.CTX_WIRE_AGENT from
// decoded config data. malformed is true when the file or the policy/set
// shapes do not decode to tables.
func codexAgentEnvValue(data []byte) (value string, present, isString, malformed bool) {
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return "", false, false, true
	}
	pv, ok := raw["shell_environment_policy"]
	if !ok {
		return "", false, false, false
	}
	policy, ok := pv.(map[string]any)
	if !ok {
		return "", false, false, true
	}
	sv, ok := policy["set"]
	if !ok {
		return "", false, false, false
	}
	set, ok := sv.(map[string]any)
	if !ok {
		return "", false, false, true
	}
	cur, ok := set[CodexAgentEnvKey]
	if !ok {
		return "", false, false, false
	}
	s, isStr := cur.(string)
	return s, true, isStr, false
}

// CodexAgentEnvConfigured reports whether config.toml at path already carries
// CTX_WIRE_AGENT = "codex" (used by doctor; config-present only, runtime proof
// is a later gain entry with agent=codex).
func CodexAgentEnvConfigured(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	v, present, isStr, malformed := codexAgentEnvValue(data)
	return !malformed && present && isStr && v == CodexAgentEnvValue
}

// InstallCodexAgentEnv upserts CTX_WIRE_AGENT = "codex" into
// shell_environment_policy.set in the config.toml at path. Idempotent; merges
// with existing inline-table or section-form set maps; preserves a
// user-modified value; fails open (CodexEnvManual, file untouched) on any
// shape it cannot confidently edit. Atomic write with .bak.
func InstallCodexAgentEnv(path string) (CodexEnvResult, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		content := "# ctx-wire: agent attribution for telemetry (sandbox-safe; not a hooks/trust change).\n" + CodexAgentEnvSnippet + "\n"
		return codexWriteLines(path, strings.Split(content, "\n"), nil, true)
	}
	if err != nil {
		return CodexEnvManual, err
	}

	v, present, isStr, malformed := codexAgentEnvValue(data)
	if malformed {
		return CodexEnvManual, nil
	}
	if present {
		if isStr && v == CodexAgentEnvValue {
			return CodexEnvNoChange, nil
		}
		return CodexEnvUserManaged, nil
	}

	lines := strings.Split(string(data), "\n")
	policyIdx, setHeaderIdx, ambiguous := codexFindAnchors(lines)
	if ambiguous {
		return CodexEnvManual, nil
	}

	switch {
	case setHeaderIdx >= 0:
		// Section form: add our key right after [shell_environment_policy.set].
		out := append([]string{}, lines[:setHeaderIdx+1]...)
		out = append(out, CodexAgentEnvKey+" = \""+CodexAgentEnvValue+"\"")
		out = append(out, lines[setHeaderIdx+1:]...)
		return codexWriteLines(path, out, data, true)
	case policyIdx >= 0:
		// Header present: merge into an inline set line, or insert one.
		end := codexSectionEnd(lines, policyIdx)
		for i := policyIdx + 1; i < end; i++ {
			if !strings.HasPrefix(strings.TrimSpace(lines[i]), "set") {
				continue
			}
			m := codexSetLineRe.FindStringSubmatch(lines[i])
			if m == nil || !codexSetPairsRe.MatchString(m[3]) {
				return CodexEnvManual, nil
			}
			inner := strings.TrimSpace(m[3])
			pair := CodexAgentEnvKey + " = \"" + CodexAgentEnvValue + "\""
			if inner == "" {
				inner = pair
			} else {
				inner += ", " + pair
			}
			lines[i] = m[1] + "set" + m[2] + "{ " + inner + " }" + m[4]
			return codexWriteLines(path, lines, data, true)
		}
		// No set in the section: insert one right after the header.
		out := append([]string{}, lines[:policyIdx+1]...)
		out = append(out, "set = { "+CodexAgentEnvKey+" = \""+CodexAgentEnvValue+"\" }")
		out = append(out, lines[policyIdx+1:]...)
		return codexWriteLines(path, out, data, true)
	default:
		// Decode says the policy table exists somewhere we cannot anchor
		// (dotted keys, exotic layout): fail open rather than risk defining
		// the table twice.
		var raw map[string]any
		_, _ = toml.Decode(string(data), &raw)
		if _, exists := raw["shell_environment_policy"]; exists {
			return CodexEnvManual, nil
		}
		// No policy anywhere: append a fresh block at EOF.
		text := strings.TrimRight(string(data), "\n")
		if text != "" {
			text += "\n\n"
		}
		text += CodexAgentEnvSnippet + "\n"
		return codexWriteLines(path, strings.Split(text, "\n"), data, true)
	}
}

// UninstallCodexAgentEnv removes CTX_WIRE_AGENT = "codex" from
// shell_environment_policy.set, and only that: a user-modified value is
// preserved (CodexEnvUserManaged), unrelated keys and comments survive, and a
// shape the scanner cannot confidently edit fails open (CodexEnvManual).
func UninstallCodexAgentEnv(path string) (CodexEnvResult, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return CodexEnvNoChange, nil
	}
	if err != nil {
		return CodexEnvManual, err
	}

	v, present, isStr, malformed := codexAgentEnvValue(data)
	if malformed {
		return CodexEnvManual, nil
	}
	if !present {
		return CodexEnvNoChange, nil
	}
	if !isStr || v != CodexAgentEnvValue {
		return CodexEnvUserManaged, nil
	}

	lines := strings.Split(string(data), "\n")
	policyIdx, setHeaderIdx, ambiguous := codexFindAnchors(lines)
	if ambiguous {
		return CodexEnvManual, nil
	}

	if setHeaderIdx >= 0 {
		end := codexSectionEnd(lines, setHeaderIdx)
		for i := setHeaderIdx + 1; i < end; i++ {
			if codexOurKeyLineRe.MatchString(strings.TrimSpace(lines[i])) {
				out := append([]string{}, lines[:i]...)
				out = append(out, lines[i+1:]...)
				return codexWriteLines(path, codexCleanupAfterRemoval(out), data, false)
			}
		}
		return CodexEnvManual, nil
	}
	if policyIdx >= 0 {
		end := codexSectionEnd(lines, policyIdx)
		for i := policyIdx + 1; i < end; i++ {
			m := codexSetLineRe.FindStringSubmatch(lines[i])
			if m == nil || !codexSetPairsRe.MatchString(m[3]) {
				continue
			}
			kept := make([]string, 0, 4)
			removed := false
			for _, p := range strings.Split(m[3], ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				if codexOurPairRe.MatchString(p) {
					removed = true
					continue
				}
				kept = append(kept, p)
			}
			if !removed {
				continue
			}
			if len(kept) == 0 {
				// The set table was ours alone: drop the whole line.
				out := append([]string{}, lines[:i]...)
				out = append(out, lines[i+1:]...)
				return codexWriteLines(path, codexCleanupAfterRemoval(out), data, false)
			}
			lines[i] = m[1] + "set" + m[2] + "{ " + strings.Join(kept, ", ") + " }" + m[4]
			return codexWriteLines(path, lines, data, false)
		}
	}
	return CodexEnvManual, nil
}

// codexCleanupAfterRemoval drops policy headers left empty by removing our key
// (plus ctx-wire's own create-comment), so uninstall leaves no residue in a
// user-owned file. A section is dropped only when nothing but blank lines
// remains in it, so user comments and keys always survive.
func codexCleanupAfterRemoval(lines []string) []string {
	lines = codexDropEmptySection(lines, codexPolicySetHeaderRe)
	lines = codexDropEmptySection(lines, codexPolicyHeaderRe)
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return append(lines, "")
}

func codexDropEmptySection(lines []string, header *regexp.Regexp) []string {
	idx := -1
	for i, l := range lines {
		if header.MatchString(strings.TrimSpace(l)) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return lines
	}
	end := codexSectionEnd(lines, idx)
	for i := idx + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return lines
		}
	}
	start := idx
	if start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "# ctx-wire: agent attribution") {
		start--
	}
	out := append([]string{}, lines[:start]...)
	return append(out, lines[end:]...)
}

// codexFindAnchors locates the [shell_environment_policy] and
// [shell_environment_policy.set] header lines. ambiguous is true when either
// appears more than once (the file would be invalid TOML, but never edit on a
// shape we do not fully understand).
func codexFindAnchors(lines []string) (policyIdx, setHeaderIdx int, ambiguous bool) {
	policyIdx, setHeaderIdx = -1, -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case codexPolicyHeaderRe.MatchString(t):
			if policyIdx >= 0 {
				return -1, -1, true
			}
			policyIdx = i
		case codexPolicySetHeaderRe.MatchString(t):
			if setHeaderIdx >= 0 {
				return -1, -1, true
			}
			setHeaderIdx = i
		}
	}
	return policyIdx, setHeaderIdx, false
}

// codexSectionEnd returns the index of the line after the section starting at
// headerIdx (the next table header, or len(lines)).
func codexSectionEnd(lines []string, headerIdx int) int {
	for i := headerIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			return i
		}
	}
	return len(lines)
}

// codexWriteLines atomically writes the edited lines, backing up the prior
// contents. Before replacing the file it verifies the edit end-to-end: the new
// text must still decode as TOML AND carry the expected post-edit
// CTX_WIRE_AGENT state (present-as-ours after install, absent after
// uninstall). A surgical edit that fails either check is never written.
func codexWriteLines(path string, lines []string, prev []byte, wantOurs bool) (CodexEnvResult, error) {
	out := strings.Join(lines, "\n")
	v, present, isStr, malformed := codexAgentEnvValue([]byte(out))
	if malformed {
		return CodexEnvManual, fmt.Errorf("refusing to write: edited config would not parse")
	}
	if ours := present && isStr && v == CodexAgentEnvValue; ours != wantOurs {
		return CodexEnvManual, fmt.Errorf("refusing to write: edited config does not carry the expected %s state", CodexAgentEnvKey)
	}
	if err := writeAtomic(path, []byte(out), len(prev) > 0); err != nil {
		return CodexEnvManual, err
	}
	return CodexEnvUpdated, nil
}
