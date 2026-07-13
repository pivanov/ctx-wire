package install

// Codex sandbox writable-root injection for gain-log durability.
//
// Codex runs wrapped commands under a workspace-write seatbelt sandbox that
// denies writes outside the cwd and $TMPDIR. ctx-wire's gain log lives at
// paths.DataHome()/ctx-wire/gain.jsonl (~/.local/share/ctx-wire on Linux/macOS),
// so under the sandbox the primary write is denied and the log silently falls
// back to a $TMPDIR copy (internal/gain.fallbackGainPath) that macOS eventually
// purges (idle janitor after ~3 days unused, or an OS update's cleanup). Result:
// codex's local gain history undercounts, and it only reaches the cloud when a
// later un-sandboxed `ctx-wire gain` merges the fallback back in.
//
// Adding ctx-wire's own data dir to [sandbox_workspace_write].writable_roots
// lets the primary write succeed durably. This grants Codex write access to
// ctx-wire's data directory only: no broader home, no repo access beyond
// normal, no ~/.codex write access.
//
// Editing is SURGICAL, never decode/re-encode: the config is user-owned and a
// round-trip would churn comments and formatting. The TOML decoder is used only
// to read state; writes are line edits on shapes the scanner positively
// recognizes. Anything ambiguous fails open to CodexSandboxManual and the caller
// prints CodexWritableRootSnippet instead. Codex 0.144.1's newer
// [permissions]/default_permissions system must not be mixed with the classic
// [sandbox_workspace_write] keys, so its presence yields CodexSandboxConflict
// (also print-only, never edit).
//
// Ownership tracking: ctx-wire tags both the writable_roots opening line (when
// it creates the array) and its own element with a marker comment. Uninstall
// removes only the element carrying the exact marker, collapses the array only
// when the opening line is ours, and collapses the [sandbox_workspace_write]
// table only when ctx-wire created it (proven by its create-comment). A path the
// user added themselves, an array the user owns, and an unrelated table's
// writable_roots are all left untouched.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ctx-wire/internal/paths"

	"github.com/BurntSushi/toml"
)

// codexSandboxMarker tags the writable_roots array and element ctx-wire manages,
// as a trailing comment, so uninstall removes only what ctx-wire added.
//
// LOAD-BEARING, DO NOT REWORD: uninstall matches this exact string in configs
// written by earlier releases. Changing it would silently orphan every existing
// ctx-wire grant (they would no longer be recognized as ours to remove). It
// fails safe (an unrecognized grant is left in place, never a wrong deletion),
// but it leaks the grant. If it ever must change, match both old and new.
const codexSandboxMarker = "ctx-wire (managed; ctx-wire uninstall removes this)"

// codexSandboxCreateComment precedes a [sandbox_workspace_write] table that
// ctx-wire created from scratch; uninstall collapses the empty table only when
// this marker proves ctx-wire owns it.
const codexSandboxCreateComment = "# ctx-wire: durable gain log under Codex's sandbox."

// CodexSandboxResult describes the outcome of a writable-root install/uninstall.
type CodexSandboxResult int

const (
	// CodexSandboxUpdated means the file was written (root added or removed).
	CodexSandboxUpdated CodexSandboxResult = iota
	// CodexSandboxNoChange means the file already carried the desired state, or
	// the path is present but user-managed (unmarked) so uninstall left it.
	CodexSandboxNoChange
	// CodexSandboxManual means the shape was not confidently editable; the
	// caller should print CodexWritableRootSnippet.
	CodexSandboxManual
	// CodexSandboxConflict means the config uses Codex's newer [permissions]
	// system, which must not be combined with the classic keys; print-only.
	CodexSandboxConflict
)

var (
	codexSWWHeaderRe = regexp.MustCompile(`^\[sandbox_workspace_write\]\s*(#.*)?$`)
	// codexRootsStartRe captures indent, the "= [" glue, and whatever follows
	// the opening bracket on the same line (a marker comment, or empty).
	codexRootsStartRe = regexp.MustCompile(`^(\s*)writable_roots(\s*=\s*)\[(.*)$`)
	// codexMarkedOpenRe matches a writable_roots opening line ctx-wire created
	// (its exact marker comment right after '['); never matches a user's line.
	codexMarkedOpenRe = regexp.MustCompile(`^\s*writable_roots\s*=\s*\[\s*#\s*` + regexp.QuoteMeta(codexSandboxMarker) + `\s*$`)
)

// CodexWritableRoot returns ctx-wire's data dir, the path that needs durable
// write access under Codex's sandbox for the gain log to survive.
func CodexWritableRoot() (string, error) {
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire"), nil
}

// CodexWritableRootSnippet is the manual fallback printed when the config shape
// cannot be confidently edited (or uses the newer [permissions] system).
func CodexWritableRootSnippet(root string) string {
	return "[sandbox_workspace_write]\nwritable_roots = [\n  \"" + codexQuotePath(root) + "\",\n]"
}

// codexQuotePath escapes a path for a TOML basic string. Backslash and quote
// come first, then the control chars TOML requires escaped. macOS/Linux paths
// pass through unchanged; a Windows path (C:\...) would otherwise interpolate
// into invalid TOML.
func codexQuotePath(s string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\t", `\t`,
		"\n", `\n`,
		"\r", `\r`,
	).Replace(s)
}

// codexWritableRoots reads sandbox_workspace_write.writable_roots from decoded
// config. malformed is true when the file or the table/array shapes do not
// decode as expected.
func codexWritableRoots(data []byte) (roots []string, tablePresent, keyPresent, newPerms, malformed bool) {
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, false, false, false, true
	}
	if _, ok := raw["permissions"]; ok {
		newPerms = true
	}
	if _, ok := raw["default_permissions"]; ok {
		newPerms = true
	}
	tv, ok := raw["sandbox_workspace_write"]
	if !ok {
		return nil, false, false, newPerms, false
	}
	tbl, ok := tv.(map[string]any)
	if !ok {
		return nil, false, false, newPerms, true
	}
	rv, ok := tbl["writable_roots"]
	if !ok {
		return nil, true, false, newPerms, false
	}
	arr, ok := rv.([]any)
	if !ok {
		return nil, true, true, newPerms, true
	}
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, true, true, newPerms, true
		}
		roots = append(roots, s)
	}
	return roots, true, true, newPerms, false
}

func codexHasRoot(roots []string, target string) bool {
	for _, r := range roots {
		if r == target {
			return true
		}
	}
	return false
}

// CodexWritableRootConfigured reports whether config.toml at path already lists
// root in sandbox_workspace_write.writable_roots. Called by doctor to surface
// codex gain durability (config-present only; the runtime proof is a codex gain
// entry landing in the primary log rather than $TMPDIR).
func CodexWritableRootConfigured(path, root string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	roots, _, _, _, malformed := codexWritableRoots(data)
	return !malformed && codexHasRoot(roots, root)
}

// SyncCodexWritableRootOnUpdate grants ctx-wire's data dir durable write access
// under Codex's sandbox after a ctx-wire version change, so an existing install
// picks up the fix without re-running `init codex`. It acts ONLY on a Codex that
// ctx-wire already manages (its hook is installed) whose config.toml already
// exists (it never creates one during a silent update). Best-effort and silent:
// a config on the newer [permissions] system, or any shape it cannot confidently
// edit, is left untouched (doctor still surfaces the gap).
//
// It returns settled: whether the outcome is stable enough to record this
// version as done. A stable outcome (grant applied, already present, not our
// codex, no config, opted out, or an uneditable/[permissions] shape) returns
// true. Only a real write/IO failure (the config dir is not writable, e.g.
// because a codex sandbox is denying the write) returns false, so the caller
// retries on the next command until the first unsandboxed run applies it. This
// makes the self-heal explicit rather than an accident of the marker sharing the
// denied data dir. settled is never "wrote the grant"; a settled result may have
// written nothing.
//
// CTX_WIRE_NO_CODEX_SANDBOX_SYNC=1 opts out of this automatic grant only;
// explicit `ctx-wire init codex` still applies it. Config writes here are not
// locked, so a concurrent manual init/uninstall on the same config.toml can
// clobber this edit; the window is a fraction of a second, doctor surfaces the
// gap, and the next version bump re-applies it (accepted tradeoff).
func SyncCodexWritableRootOnUpdate() (settled bool) {
	if codexSandboxSyncDisabled() {
		return true // opted out: no-op, and nothing to retry
	}
	hooksPath, err := CodexHooksPath()
	if err != nil {
		return true
	}
	if !CodexHookInstalled(hooksPath) {
		return true // ctx-wire does not manage this Codex; nothing to do
	}
	cfgPath, err := CodexConfigPath()
	if err != nil {
		return true
	}
	if _, err := os.Stat(cfgPath); err != nil {
		return true // no config.toml: never create one on a silent update
	}
	root, err := CodexWritableRoot()
	if err != nil {
		return true
	}
	_, werr := InstallCodexWritableRoot(cfgPath, root)
	// werr is non-nil only on a read/write I/O failure (a sandbox denying the
	// write); every stable shape outcome (Updated, NoChange, Conflict, Manual)
	// returns nil. Retry only the transient I/O case.
	return werr == nil
}

// codexSandboxSyncDisabled reports whether the user opted out of the automatic
// post-update writable-root grant via CTX_WIRE_NO_CODEX_SANDBOX_SYNC. It gates
// only the silent auto-sync, not explicit `ctx-wire init codex`.
func codexSandboxSyncDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CTX_WIRE_NO_CODEX_SANDBOX_SYNC"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// InstallCodexWritableRoot adds root to sandbox_workspace_write.writable_roots
// in the config.toml at path. Idempotent; merges with an existing multiline
// array; preserves a user-added copy of the same path (marker-tagged additions
// only); fails open (CodexSandboxManual, file untouched) on any shape it cannot
// confidently edit, and refuses to touch a config on Codex's newer [permissions]
// system (CodexSandboxConflict). Atomic write with .bak.
func InstallCodexWritableRoot(path, root string) (CodexSandboxResult, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return codexWriteSandboxLines(path, strings.Split(codexFreshBlock(root), "\n"), nil, root, true)
	}
	if err != nil {
		return CodexSandboxManual, err
	}

	roots, tablePresent, keyPresent, newPerms, malformed := codexWritableRoots(data)
	if malformed {
		return CodexSandboxManual, nil
	}
	if newPerms {
		return CodexSandboxConflict, nil
	}
	if codexHasRoot(roots, root) {
		return CodexSandboxNoChange, nil
	}

	lines := strings.Split(string(data), "\n")
	hdrIdx, ambiguous := codexFindSWWHeader(lines)
	if ambiguous {
		return CodexSandboxManual, nil
	}

	switch {
	case !tablePresent:
		// No table anywhere: append a fresh block at EOF.
		text := strings.TrimRight(string(data), "\n")
		if text != "" {
			text += "\n\n"
		}
		text += codexFreshBlock(root)
		return codexWriteSandboxLines(path, strings.Split(text, "\n"), data, root, true)

	case !keyPresent:
		if hdrIdx < 0 {
			// Table decoded but its header cannot be anchored (dotted-key
			// form): fail open rather than risk defining it twice.
			return CodexSandboxManual, nil
		}
		// ctx-wire owns the array it is creating: mark the opening line.
		out := append([]string{}, lines[:hdrIdx+1]...)
		out = append(out, codexCreatedArrayLines(root)...)
		out = append(out, lines[hdrIdx+1:]...)
		return codexWriteSandboxLines(path, out, data, root, true)

	default:
		// Table + writable_roots present: append our marked element to the
		// user's existing array (opening line left untouched, so uninstall
		// knows the array is not ours to collapse).
		if hdrIdx < 0 {
			return CodexSandboxManual, nil
		}
		return codexAppendRootToArray(path, lines, data, hdrIdx, root)
	}
}

// UninstallCodexWritableRoot removes the ctx-wire-managed root, and only that:
// a copy of the same path the user added without the exact marker is preserved,
// a user-owned array or unrelated table is never touched, and a shape the
// scanner cannot confidently edit fails open (CodexSandboxManual).
func UninstallCodexWritableRoot(path, root string) (CodexSandboxResult, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return CodexSandboxNoChange, nil
	}
	if err != nil {
		return CodexSandboxManual, err
	}
	roots, _, _, _, malformed := codexWritableRoots(data)
	if malformed {
		return CodexSandboxManual, nil
	}
	if !codexHasRoot(roots, root) {
		return CodexSandboxNoChange, nil
	}

	lines := strings.Split(string(data), "\n")
	hdrIdx, ambiguous := codexFindSWWHeader(lines)
	if ambiguous {
		return CodexSandboxManual, nil
	}
	if hdrIdx < 0 {
		// ctx-wire never writes into a header-less (dotted-key) shape, so a
		// marked element cannot be there; nothing to safely remove.
		return CodexSandboxManual, nil
	}
	end := codexSectionEnd(lines, hdrIdx)

	// Find our marked element, scoped to the sandbox section.
	elemRe := codexMarkedElementRe(root)
	elemIdx := -1
	for i := hdrIdx + 1; i < end; i++ {
		if elemRe.MatchString(lines[i]) {
			if elemIdx >= 0 {
				return CodexSandboxManual, nil // two marked lines: fail open
			}
			elemIdx = i
		}
	}
	if elemIdx < 0 {
		// Present but not ctx-wire's marked element: the user added it; leave it.
		return CodexSandboxNoChange, nil
	}
	// Only collapse the array if ctx-wire created it (marked opening line).
	ownArray := false
	for i := hdrIdx + 1; i < end; i++ {
		if codexMarkedOpenRe.MatchString(lines[i]) {
			ownArray = true
			break
		}
	}

	out := append([]string{}, lines[:elemIdx]...)
	out = append(out, lines[elemIdx+1:]...)
	if ownArray {
		out = codexDropOwnedRootsArray(out)
		out = codexDropOwnedEmptySWWSection(out)
		out = codexTrimTrailingBlanks(out)
	}
	return codexWriteSandboxLines(path, out, data, root, false)
}

func codexFreshBlock(root string) string {
	return codexSandboxCreateComment + "\n[sandbox_workspace_write]\n" + strings.Join(codexCreatedArrayLines(root), "\n") + "\n"
}

// codexCreatedArrayLines is the writable_roots array ctx-wire writes when it
// owns the array: the opening line carries the marker so uninstall can tell it
// apart from a user's array.
func codexCreatedArrayLines(root string) []string {
	return []string{
		"writable_roots = [ # " + codexSandboxMarker,
		codexElementLine(root),
		"]",
	}
}

func codexElementLine(root string) string {
	return "  \"" + codexQuotePath(root) + "\", # " + codexSandboxMarker
}

// codexMarkedElementRe matches a writable_roots element line for root that
// carries ctx-wire's exact marker comment. It never matches an unmarked user
// line, nor a user comment that merely mentions ctx-wire.
func codexMarkedElementRe(root string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*"` + regexp.QuoteMeta(codexQuotePath(root)) + `"\s*,?\s*#\s*` + regexp.QuoteMeta(codexSandboxMarker) + `\s*$`)
}

// codexFindSWWHeader locates the single [sandbox_workspace_write] header.
// ambiguous is true when it appears more than once (invalid TOML, but never
// edit a shape we do not fully understand).
func codexFindSWWHeader(lines []string) (idx int, ambiguous bool) {
	idx = -1
	for i, l := range lines {
		if codexSWWHeaderRe.MatchString(strings.TrimSpace(l)) {
			if idx >= 0 {
				return -1, true
			}
			idx = i
		}
	}
	return idx, false
}

// codexAppendRootToArray inserts our marked element before the closing bracket
// of the user's existing multiline writable_roots array. An inline (single-line)
// array fails open to Manual: a '#' marker inside a one-line [ ... ] would
// comment out the closing bracket.
func codexAppendRootToArray(path string, lines []string, prev []byte, hdrIdx int, root string) (CodexSandboxResult, error) {
	end := codexSectionEnd(lines, hdrIdx)
	for i := hdrIdx + 1; i < end; i++ {
		m := codexRootsStartRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if strings.Contains(codexStripLineComment(m[3]), "]") {
			return CodexSandboxManual, nil // inline array
		}
		for j := i + 1; j < end; j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "]") {
				out := append([]string{}, lines[:j]...)
				out = append(out, codexElementLine(root))
				out = append(out, lines[j:]...)
				return codexWriteSandboxLines(path, out, prev, root, true)
			}
		}
		return CodexSandboxManual, nil // opened but never closed in-section
	}
	return CodexSandboxManual, nil // decoded present but not scanner-visible
}

// codexDropOwnedRootsArray removes the writable_roots array ctx-wire created
// (identified by its marked opening line) when it is now empty. A user-owned
// array has no marked opening line, so this never touches it, and the marker
// scopes the search so an unrelated table's writable_roots is never seen.
func codexDropOwnedRootsArray(lines []string) []string {
	for i, l := range lines {
		if !codexMarkedOpenRe.MatchString(l) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if !strings.HasPrefix(strings.TrimSpace(lines[j]), "]") {
				if strings.TrimSpace(lines[j]) != "" {
					return lines // array still has elements
				}
				continue
			}
			out := append([]string{}, lines[:i]...)
			return append(out, lines[j+1:]...)
		}
		return lines
	}
	return lines
}

// codexDropOwnedEmptySWWSection collapses a now-empty [sandbox_workspace_write]
// table, but only one ctx-wire created, proven by its create-comment. A table
// the user wrote (even if left empty) keeps its header.
func codexDropOwnedEmptySWWSection(lines []string) []string {
	idx := -1
	for i, l := range lines {
		if codexSWWHeaderRe.MatchString(strings.TrimSpace(l)) {
			idx = i
			break
		}
	}
	if idx <= 0 || strings.TrimSpace(lines[idx-1]) != codexSandboxCreateComment {
		return lines
	}
	end := codexSectionEnd(lines, idx)
	for i := idx + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return lines // section has other content
		}
	}
	out := append([]string{}, lines[:idx-1]...)
	return append(out, lines[end:]...)
}

func codexTrimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return append(lines, "")
}

// codexStripLineComment drops a trailing '# ...' comment that is outside a
// double-quoted string, so inline-vs-multiline detection ignores comment text.
func codexStripLineComment(s string) string {
	inStr := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inStr = !inStr
		case '#':
			if !inStr {
				return s[:i]
			}
		}
	}
	return s
}

// codexWriteSandboxLines atomically writes the edited lines with a .bak backup,
// but only after verifying end-to-end that the result still decodes as TOML and
// carries the expected post-edit writable_roots state (root present after
// install, absent after uninstall). A surgical edit that fails either check is
// never written.
func codexWriteSandboxLines(path string, lines []string, prev []byte, root string, wantPresent bool) (CodexSandboxResult, error) {
	out := strings.Join(lines, "\n")
	roots, _, _, _, malformed := codexWritableRoots([]byte(out))
	if malformed {
		return CodexSandboxManual, fmt.Errorf("refusing to write: edited config would not parse")
	}
	if has := codexHasRoot(roots, root); has != wantPresent {
		return CodexSandboxManual, fmt.Errorf("refusing to write: edited config does not carry the expected writable_roots state")
	}
	if err := writeAtomic(path, []byte(out), len(prev) > 0); err != nil {
		return CodexSandboxManual, err
	}
	return CodexSandboxUpdated, nil
}
