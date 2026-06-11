package rewrite

// ContainsUnattestableConstruct reports whether line hides a command that
// ctx-wire cannot attest. The rewriter refuses to wrap such a line, which keeps
// every hook from emitting an auto-allow for it: the host agent then evaluates
// the ORIGINAL command, with the construct visible, instead of ctx-wire vouching
// (via a wrapped "allow") for something it never inspected.
//
// Without this, a segment like `git log --pretty=$(rm -rf ~)` reads as a `git`
// command, passes the hook's command-level deny/ask check, and is auto-approved,
// suppressing the prompt the agent would otherwise show for the embedded `rm`.
//
// It flags exactly the command-hiding constructs the rewrite splitter and the
// permission segment-splitter do NOT already handle:
//   - command substitution: $(...) and `...`
//   - process substitution: <(...) and >(...)
//   - a second top-level command smuggled past `;`/`&&`/`||` splitting via a
//     newline or a background `&` that is followed by another command
//
// Quote- and escape-aware: nothing inside single quotes is flagged, and an
// escaped form like "\$(cmd)" is not a substitution. Plain variable expansion
// ($VAR, ${VAR}) executes no command and is not flagged. Plain file redirects
// (> f, 2>&1, < f) are intentionally NOT flagged here: they hide no command and
// the rewriter already passes them through.
func ContainsUnattestableConstruct(line string) bool {
	n := len(line)
	var quote byte // 0, '\'' (single) or '"' (double)
	for i := 0; i < n; i++ {
		c := line[i]
		switch {
		case quote == '\'':
			// Single quotes are literal: nothing expands until the closing quote.
			if c == '\'' {
				quote = 0
			}
		case c == '\\':
			i++ // backslash escapes the next byte (outside single quotes)
		case quote == '"':
			// Inside double quotes, command substitution still expands.
			switch {
			case c == '"':
				quote = 0
			case c == '`':
				return true
			case isCommandSub(line, i):
				return true
			}
		case c == '\'':
			quote = '\''
		case c == '"':
			quote = '"'
		case c == '`':
			return true
		case isCommandSub(line, i):
			return true
		case (c == '<' || c == '>') && i+1 < n && line[i+1] == '(':
			return true // process substitution
		case c == '\n':
			if hasCommandAfter(line, i+1) {
				return true // a second top-level command after a newline
			}
		case c == '&':
			switch {
			case i+1 < n && line[i+1] == '&':
				i++ // '&&' is a segment operator, handled by the splitter
			case i+1 < n && line[i+1] == '>':
				// '&>' is a redirect (hides no command), left to redirect handling.
			case i > 0 && (line[i-1] == '>' || line[i-1] == '<'):
				// '>&' / '<&' is an fd dup (2>&1, >&2), not a command separator.
			default:
				if hasCommandAfter(line, i+1) {
					return true // 'cmd & cmd' smuggles a second command
				}
			}
		}
	}
	return false
}

// ContainsRedirect reports whether line contains a shell I/O redirection to a
// path (`>`, `>>`, `2>`, `&>`, `<`), which writes or reads an arbitrary path and
// so does more than the command appears. File-descriptor dups (`>&1`, `2>&1`)
// target an fd, not a path, and are not flagged. Quoted/escaped operators are
// literal and ignored. The codex permission gate uses this as defense in depth
// behind the rewriter's redirect passthrough, so a wrapped safe program
// (`cat x > /dev/sda`) never auto-approves an arbitrary write.
func ContainsRedirect(line string) bool {
	n := len(line)
	var quote byte
	for i := 0; i < n; i++ {
		c := line[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '\\':
			i++ // escape next byte (outside single quotes)
		case quote == '"':
			if c == '"' {
				quote = 0
			}
		case c == '\'':
			quote = '\''
		case c == '"':
			quote = '"'
		case c == '<' || c == '>':
			j := i + 1
			for j < n && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			if j >= n || line[j] != '&' { // not an fd dup → path redirect
				return true
			}
		}
	}
	return false
}

// isCommandSub reports whether a command substitution `$(` begins at index i.
// It excludes arithmetic expansion `$((`, which executes no command. A command
// substitution nested inside arithmetic (e.g. `$(( $(id) ))`) is still flagged
// when the scan reaches its own `$(`, since this only skips the `$((` opener.
func isCommandSub(line string, i int) bool {
	if line[i] != '$' || i+1 >= len(line) || line[i+1] != '(' {
		return false
	}
	return i+2 >= len(line) || line[i+2] != '(' // not `$((` arithmetic
}

// hasCommandAfter reports whether anything other than whitespace and newlines
// remains at or after index k. A separator (newline or '&') with content after
// it introduces a second command; a trailing one is a benign terminator.
func hasCommandAfter(line string, k int) bool {
	for ; k < len(line); k++ {
		switch line[k] {
		case ' ', '\t', '\n', '\r':
		default:
			return true
		}
	}
	return false
}
