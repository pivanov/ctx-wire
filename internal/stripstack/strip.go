// Package stripstack collapses runs of third-party / runtime stack frames in
// command output, leaving the exception header, every application frame, and
// inter-exception links ("caused by", "during handling") intact.
//
// It is an opt-in, subtractive post-filter. It only hides a frame whose source
// location is provably a library or language-runtime path, so it cannot remove
// the application frame that is usually the answer: an unrecognized frame is
// treated as application code and kept. A run is collapsed only when it has at
// least two consecutive library frames, and the runner spools the full raw trace
// to disk, so nothing is ever lost.
//
// Classification is deliberately strict to avoid hiding application code that
// merely resembles a library path:
//   - Node: a canonical `at func (LOC)` frame is library when LOC starts with a
//     Node built-in (`node:<letter>`, not a `node:<ip>` log line) or contains a
//     `/node_modules/` path segment. A bare `at LOC` frame (no parens) is library
//     only for the `node:<letter>` builtin form, because a bare node_modules path
//     is textually indistinguishable from a stray `at <path>:line:col` log line.
//   - Python: `/site-packages/`, `/dist-packages/`, or `<frozen ...>`. A bare
//     stdlib path like `/lib/python3.11/` is intentionally NOT matched: app layouts
//     reuse `lib/pythonN` directory names, so site-packages is the only safe signal.
//   - Java/JVM: package `java.*` (JVM-reserved), `jdk.internal.*`, the `sun.*`
//     internal roots, and the Kotlin stdlib runtime subpackages. App namespaces
//     like `com.x.reflect`, `sun.mycompany`, or `kotlin.mycompany` are NOT hidden.
//
// Known v1 limitations (raw trace is always spooled, so these never lose data):
//   - A monorepo whose own packages run from under `/node_modules/` will have
//     those frames collapsed (the standard node_modules convention is ambiguous).
//   - A directory literally named `dist-packages` is treated as the system one.
//   - Go and Rust backtraces are not yet recognized and pass through untouched.
package stripstack

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	// `  File "/path/x.py", line 12, in func`
	pyFileRe = regexp.MustCompile(`^(\s*)File "([^"]*)", line \d+, in `)
	// `    at fn (/path/file.js:10:5)` / `    at /path:10:5` / `    at node:internal/...:1:2`.
	// Distinguished from Java frames by the trailing `:line:col` (two numbers).
	jsFrameRe = regexp.MustCompile(`^(\s*)at\s+\S.*:\d+:\d+\)?\s*$`)
	// `	at pkg.Class.method(File.java:12)` / `(Native Method)` / `(Unknown Source)`
	javaFrameRe = regexp.MustCompile(`^(\s*)at\s+([\w$.<>/]+)\([^()]*\)\s*$`)
	leadWSRe    = regexp.MustCompile(`^\s*`)

	// jsParenRe extracts a V8 frame's parenthesized location: `... (LOC)`.
	jsParenRe = regexp.MustCompile(`\(([^()]*)\)\s*$`)
	// jsNodeBuiltinRe matches a Node built-in module location: `node:` then a
	// lowercase module name (the Node convention, e.g. internal, fs, child_process)
	// then `/` (submodule) or `:` (line:col). This rejects log lines like
	// `node:10.0.0.1:8080:0` (digit) and `node:control-plane:6443:0` (hyphen host).
	jsNodeBuiltinRe = regexp.MustCompile(`^node:[a-z_]+[/:]`)
)

// javaLibPrefixes are the JVM/JDK/Kotlin-stdlib package roots that are never
// application code. java.* is JVM-reserved (the classloader forbids app classes
// there); jdk.internal.* and the sun.* roots are JDK internals; the kotlin.*
// entries are stdlib runtime subpackages (a bare `kotlin.` prefix would also
// match an app namespace, so the specific subpackages are used instead).
var javaLibPrefixes = []string{
	"java.",
	"jdk.internal.",
	"sun.reflect.", "sun.launcher.", "sun.misc.", "sun.nio.", "sun.security.", "sun.net.",
	"kotlin.coroutines.", "kotlin.jvm.internal.",
}

var enabled bool

// SetEnabled sets the configured default (from [output] strip_stacktraces).
func SetEnabled(v bool) { enabled = v }

// Enabled reports whether stripping is on. CTX_WIRE_STRIP_STACKTRACES overrides
// the configured default per invocation: 1/true/on/yes enable, 0/false/off/no
// disable.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CTX_WIRE_STRIP_STACKTRACES"))) {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	}
	return enabled
}

func isPyLib(path string) bool {
	return strings.Contains(path, "/site-packages/") ||
		strings.Contains(path, "/dist-packages/") ||
		strings.HasPrefix(path, "<frozen ")
}

// isJSLib classifies a V8 frame line. A canonical `at func (LOC)` frame is library
// when LOC starts with a Node built-in (`node:<letter>`) or contains a
// `/node_modules/` path segment. A bare `at LOC` frame (no parens) is classified
// library only for the unambiguous `node:<letter>` builtin form: a bare
// `at /x/node_modules/y.js:1:1` is textually identical to a stray `at <path>:N:N`
// log/prose line, so it is left alone (a miss, never a corruption; real traces use
// the parenthesized form, which still collapses).
func isJSLib(line string) bool {
	if m := jsParenRe.FindStringSubmatch(line); m != nil {
		loc := m[1]
		return jsNodeBuiltinRe.MatchString(loc) || strings.Contains(loc, "/node_modules/")
	}
	bare := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "at "))
	return jsNodeBuiltinRe.MatchString(bare)
}

func isJavaLib(pkg string) bool {
	if i := strings.Index(pkg, "/"); i >= 0 {
		pkg = pkg[i+1:] // module-qualified: "java.base/java.lang..." -> "java.lang..."
	}
	for _, p := range javaLibPrefixes {
		if strings.HasPrefix(pkg, p) {
			return true
		}
	}
	return false
}

// matchFrame reports whether a stack frame begins at lines[i]. It returns the
// number of lines the frame occupies (1, or 2 for a Python File+source pair),
// whether it is a library/runtime frame, and the frame's indent.
func matchFrame(lines []string, i int) (consumed int, isLib bool, indent string, ok bool) {
	ln := lines[i]
	if m := pyFileRe.FindStringSubmatch(ln); m != nil {
		indent = m[1]
		isLib = isPyLib(m[2])
		consumed = 1
		// Pair with the following source line when present: more indented than
		// the File line, non-blank, and not itself a frame of any language (so a
		// mixed-language trace, e.g. JPype's Java frame under a Python frame, is
		// not swallowed).
		if i+1 < len(lines) {
			nxt := lines[i+1]
			if strings.TrimSpace(nxt) != "" &&
				len(leadWSRe.FindString(nxt)) > len(indent) &&
				pyFileRe.FindStringSubmatch(nxt) == nil &&
				!jsFrameRe.MatchString(nxt) &&
				!javaFrameRe.MatchString(nxt) {
				consumed = 2
			}
		}
		return consumed, isLib, indent, true
	}
	if m := jsFrameRe.FindStringSubmatch(ln); m != nil {
		return 1, isJSLib(ln), m[1], true
	}
	if m := javaFrameRe.FindStringSubmatch(ln); m != nil {
		return 1, isJavaLib(m[2]), m[1], true
	}
	return 0, false, "", false
}

// Strip collapses each run of >=2 consecutive library frames into a single
// "... (+N library frames hidden)" marker, keeping application frames, the
// exception header, and every non-frame line verbatim. It returns the rewritten
// text and whether anything was collapsed. Strip is pure; gating is the caller's
// job (see Enabled).
func Strip(s string) (string, bool) {
	if s == "" {
		return s, false
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	var libBuf []string
	libCount := 0
	indent := ""
	changed := false
	flush := func() {
		if libCount >= 2 {
			out = append(out, indent+"... (+"+strconv.Itoa(libCount)+" library frames hidden)")
			changed = true
		} else {
			out = append(out, libBuf...)
		}
		libBuf = libBuf[:0]
		libCount = 0
	}
	for i := 0; i < len(lines); {
		consumed, isLib, ind, ok := matchFrame(lines, i)
		if ok && isLib {
			if libCount == 0 {
				indent = ind
			}
			libBuf = append(libBuf, lines[i:i+consumed]...)
			libCount++
			i += consumed
			continue
		}
		flush()
		if ok {
			out = append(out, lines[i:i+consumed]...) // application frame, kept
			i += consumed
			continue
		}
		out = append(out, lines[i]) // non-frame line, kept
		i++
	}
	flush()
	if !changed {
		return s, false
	}
	return strings.Join(out, "\n"), true
}
