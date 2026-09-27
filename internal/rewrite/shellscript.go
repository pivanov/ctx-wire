package rewrite

import "strings"

// ShellScriptArgs rewrites the -c script of a shell invocation that reached the
// runner as argv, e.g. `ctx-wire run sh -lc 'git status && git diff'` typed by
// an agent. The hook never sees inside that form (the line is already wrapped),
// so without this the whole script runs as one unfiltered passthrough. The
// script arrives already unquoted by the calling shell, so no re-quoting of the
// -c word is needed: each segment is wrapped with wrap exactly as a top-level
// line would be, under the same conservative rules (lineWith bails on command
// substitution, newlines and background jobs). ok is false when args is not a
// plain `<shell> [flags] -c <script>` form or nothing inside is wrappable.
func ShellScriptArgs(name string, args []string, wrap string) (rewritten []string, ok bool) {
	if !isShellProgram(name) {
		return nil, false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") {
			return nil, false
		}
		if shellOptionTakesArg(a) {
			i++
			continue
		}
		if !isShellCommandFlag(a) {
			continue
		}
		if i+1 >= len(args) {
			return nil, false
		}
		script := args[i+1]
		out := lineWith(script, wrap)
		if out == script {
			return nil, false
		}
		rewritten = append([]string(nil), args...)
		rewritten[i+1] = out
		return rewritten, true
	}
	return nil, false
}
