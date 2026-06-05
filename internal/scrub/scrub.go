// Package scrub redacts secrets from command argv and output before any value
// is summarized or persisted to disk. It is the security floor described in the
// plan: scrubbing runs unconditionally, and callers persisting output use
// ScrubFailClosed so that a redaction failure withholds the output rather than
// risking a leak.
//
// Regex-based scrubbing is a strong reduction, not a mathematical guarantee:
// novel or heavily obfuscated secrets can slip through. Patterns target the
// common high-confidence shapes (cloud keys, provider tokens, JWTs, PEM private
// keys, Authorization headers, secret-ish assignments, and URL userinfo).
package scrub

import (
	"regexp"
	"strings"
)

// redacted is the placeholder substituted for a detected secret.
const redacted = "[REDACTED]"

// rule is a single redaction pass. replacement is a regexp replacement template:
// the whole match is replaced, so capture groups ($1, $2) re-emit any prefix or
// suffix that must be preserved around the redacted value.
type rule struct {
	name        string
	re          *regexp.Regexp
	replacement string
}

// rules are applied in order. PEM (multi-line) runs first; the many single-line
// token shapes are merged into one alternation so the common case is a single
// regex pass (cheaper on large output); the group-capturing rules run last.
var rules = []rule{
	{name: "pem-private-key", re: regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), replacement: redacted},
	// Single-line high-confidence token shapes (JWT, cloud keys, provider
	// tokens), all redacted whole, combined into one pass.
	{name: "tokens", re: regexp.MustCompile(
		`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+` + // JWT
			`|\b(?:AKIA|ASIA)[0-9A-Z]{16}\b` + // AWS access key
			`|\bAIza[0-9A-Za-z_\-]{35}\b` + // Google API key
			`|\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b` + // GitHub token
			`|\bgithub_pat_[A-Za-z0-9_]{22,}\b` + // GitHub fine-grained PAT
			`|\bxox[baprs]-[A-Za-z0-9-]{10,}\b` + // Slack token
			`|\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b` + // Stripe key
			`|\bsk-(?:ant-)?[A-Za-z0-9_\-]{20,}\b`, // OpenAI / Anthropic key
	), replacement: redacted},
	// Authorization: Bearer <token> / Basic <token> / token <token>
	{name: "authorization-header", re: regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer|basic|token)\s+)\S+`), replacement: "${1}" + redacted},
	// Split long flags whose following argv value is a secret. This is a
	// defense-in-depth pass for older persisted command samples; new command
	// records are scrubbed argv-aware by Command before they reach disk.
	{name: "secret-flag-value", re: regexp.MustCompile(`(?i)(--(?:password|passwd|pwd|secret|token|auth[_-]?token|access[_-]?token|api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|client[_-]?secret|credential|credentials)\s+)('[^']*'|"[^"]*"|[^\s]+)`), replacement: "${1}" + redacted},
	// scheme://user:password@host -> redact only the password, keep the rest
	{name: "url-userinfo", re: regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^\s:/@]+:)[^\s@/]+(@)`), replacement: "${1}" + redacted + "${2}"},
	// secret-ish key = value (and key: value). Keep the key, redact the value.
	// The value alternation captures single-quoted, double-quoted (which may
	// contain spaces), or bare tokens, so the whole secret is redacted.
	{name: "secret-assignment", re: regexp.MustCompile(`(?i)((?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|auth[_-]?token|client[_-]?secret)\s*[:=]\s*)('[^']*'|"[^"]*"|[^\s]+)`), replacement: "${1}" + redacted},
}

// Scrub redacts known secret shapes from s. It never returns an error and is
// safe on empty input.
func Scrub(s string) string {
	if s == "" || !mightContainSecret(s) {
		return s
	}
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.replacement)
	}
	return s
}

// literalAnchors are case-sensitive substrings that every token/PEM/URL rule
// requires. keywordRoots are case-insensitive substrings the assignment and
// authorization rules require.
var (
	literalAnchors = []string{
		"eyJ", "AKIA", "ASIA", "AIza", "ghp_", "gho_", "ghu_", "ghs_", "ghr_",
		"github_pat_", "xox", "sk_", "rk_", "sk-", "-----BEGIN", "://",
	}
	keywordRoots = []string{
		"passw", "pwd", "secret", "token", "api", "key", "auth", "client", "access", "private",
	}
)

// mightContainSecret is a cheap pre-filter: it reports whether s contains any
// marker that some redaction rule could match. When it returns false, the
// expensive regex passes are skipped entirely. It is a strict superset of the
// rule triggers, so it never causes a real secret to be skipped.
func mightContainSecret(s string) bool {
	for _, a := range literalAnchors {
		if strings.Contains(s, a) {
			return true
		}
	}
	lower := strings.ToLower(s)
	for _, kw := range keywordRoots {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ScrubArgs redacts secrets from each argv element, returning a new slice.
func ScrubArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = Scrub(a)
	}
	return out
}

// secretFlagNames are long-flag names whose following argv value is a secret.
// Keys are normalized: lowercased with '-' and '_' removed (so --api-key,
// --api_key, and --apikey all match).
var secretFlagNames = map[string]bool{
	"password": true, "passwd": true, "pwd": true,
	"secret": true, "token": true, "authtoken": true, "accesstoken": true,
	"apikey": true, "accesskey": true, "secretkey": true, "privatekey": true,
	"clientsecret": true, "credential": true, "credentials": true,
}

// isSecretFlag reports whether a is a bare long flag (e.g. --password) whose
// following argv element holds a secret value. Inline forms (--flag=value) and
// short flags (-p) are intentionally excluded: inline values are caught by the
// string scrubber, and short flags are too ambiguous to classify safely.
func isSecretFlag(a string) bool {
	if !strings.HasPrefix(a, "--") || strings.Contains(a, "=") {
		return false
	}
	name := strings.ToLower(strings.TrimLeft(a, "-"))
	name = strings.ReplaceAll(name, "-", "")
	name = strings.ReplaceAll(name, "_", "")
	return secretFlagNames[name]
}

// Command builds a scrubbed, displayable command string from a program name and
// its argv. It is argv-aware: in addition to the inline redaction Scrub does
// (--flag=value, KEY=value, token shapes), it redacts the value that follows a
// secret-ish long flag passed as a separate argument (e.g. `--password hunter2`
// becomes `--password [REDACTED]`). Use this for any place a command line is
// persisted or displayed.
func Command(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(Scrub(name)))
	redactNext := false
	for _, a := range args {
		switch {
		case redactNext:
			parts = append(parts, shellQuote(redacted))
			redactNext = false
		case isSecretFlag(a):
			parts = append(parts, shellQuote(a))
			redactNext = true
		default:
			parts = append(parts, shellQuote(Scrub(a)))
		}
	}
	return strings.Join(parts, " ")
}

// CommandLine canonicalizes a raw shell command line into the same form Command
// produces from an exec's argv, so a transcript command can be matched against a
// gain record (which was stored via Command at run time). It tokenizes line into
// argv (stripping one level of shell quoting, as a shell would) and re-renders it
// through Command, applying identical scrubbing and secret redaction. It is a
// diagnostic-grade tokenizer, not a full shell parser; an empty tokenization
// falls back to scrubbing the raw line.
func CommandLine(line string) string {
	argv := tokenizeShell(line)
	if len(argv) == 0 {
		return Scrub(strings.TrimSpace(line))
	}
	return Command(argv[0], argv[1:])
}

// tokenizeShell splits a command line into argv, honoring single quotes (literal),
// double quotes (with backslash escapes for " \ $ `), and backslash escapes
// outside quotes. It strips one level of quoting, matching how a shell hands argv
// to a program, so the result lines up with the argv gain recorded.
func tokenizeShell(line string) []string {
	var args []string
	var cur strings.Builder
	inWord := false
	var quote rune
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inWord = true
		case quote == '"':
			if r == '\\' && i+1 < len(runes) {
				switch runes[i+1] {
				case '"', '\\', '$', '`':
					cur.WriteRune(runes[i+1])
					i++
				default:
					cur.WriteRune(r)
				}
			} else if r == '"' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inWord = true
		case r == '\\' && i+1 < len(runes):
			cur.WriteRune(runes[i+1])
			i++
			inWord = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !isShellSafe(r) {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

func isShellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '_', '@', '%', '+', '=', ':', ',', '.', '/', '-':
		return true
	}
	return false
}

// ScrubFailClosed scrubs s for callers that persist output. If scrubbing panics
// for any reason, it returns ok=false so the caller can withhold the data
// rather than write a potentially unredacted value to disk.
func ScrubFailClosed(s string) (out string, ok bool) {
	return scrubFailClosedWith(s, Scrub)
}

func scrubFailClosedWith(s string, scrub func(string) string) (out string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			out, ok = "", false
		}
	}()
	return scrub(s), true
}
