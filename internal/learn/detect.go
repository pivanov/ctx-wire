package learn

import (
	"regexp"
	"strings"
)

// ErrorKind is a coarse classification of a failed command, used to label and
// group corrections. It mirrors the categories that make for actionable CLI
// advice (a bad flag, a missing argument) rather than logic bugs.
type ErrorKind string

const (
	ErrUnknownFlag      ErrorKind = "unknown flag"
	ErrCommandNotFound  ErrorKind = "command not found"
	ErrMissingArg       ErrorKind = "missing argument"
	ErrPermissionDenied ErrorKind = "permission denied"
	ErrWrongPath        ErrorKind = "wrong path"
	ErrGeneral          ErrorKind = "general error"
)

var (
	reUnknownFlag = regexp.MustCompile(`(?i)(unexpected argument|unknown (option|flag)|unrecognized (option|flag)|invalid (option|flag))`)
	reCmdNotFound = regexp.MustCompile(`(?i)(command not found|not recognized as an internal|is not recognized as)`)
	reWrongPath   = regexp.MustCompile(`(?i)(no such file or directory|cannot find the path|file not found)`)
	reMissingArg  = regexp.MustCompile(`(?i)(requires a value|requires an argument|missing (required )?argument|expected.*argument)`)
	rePermission  = regexp.MustCompile(`(?i)(permission denied|access denied|not permitted)`)
	// User rejections (declined a command) are not CLI errors.
	reUserReject = regexp.MustCompile(`(?i)(user (doesn't want|declined|rejected|cancelled)|operation (cancelled|aborted) by user)`)
)

// CorrectionWindow is how many subsequent commands to scan for a fix after a
// failure. Beyond this, a later command is unlikely to be a direct correction.
const CorrectionWindow = 3

// MinConfidence is the floor a correction pair must clear to be reported.
const MinConfidence = 0.6

// maxCommandLen bounds the length of a command considered for a correction rule.
// A correction is about CLI usage (a flag, a subcommand, a typo), so a giant
// inline script or eval payload is never a useful rule and only adds noise.
const maxCommandLen = 256

// isSimpleCommand reports whether cmd is a plain, single-line CLI invocation
// short enough to be a meaningful correction rule (not a multi-line script or a
// long eval/here-doc payload).
func isSimpleCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	return cmd != "" && len(cmd) <= maxCommandLen && !strings.ContainsAny(cmd, "\n\r")
}

// isCommandError reports whether a failed execution looks like a genuine CLI
// error worth learning from: it must be flagged as an error, carry
// error-indicating output, and not be a user rejection.
func isCommandError(e Execution) bool {
	if !e.IsError {
		return false
	}
	if reUserReject.MatchString(e.Output) {
		return false
	}
	low := strings.ToLower(e.Output)
	for _, marker := range []string{"error", "failed", "unknown", "invalid", "not found", "permission denied", "cannot"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

// classifyError maps error output to a coarse ErrorKind.
func classifyError(output string) ErrorKind {
	switch {
	case reUnknownFlag.MatchString(output):
		return ErrUnknownFlag
	case reCmdNotFound.MatchString(output):
		return ErrCommandNotFound
	case reMissingArg.MatchString(output):
		return ErrMissingArg
	case rePermission.MatchString(output):
		return ErrPermissionDenied
	case reWrongPath.MatchString(output):
		return ErrWrongPath
	default:
		return ErrGeneral
	}
}

// isTDDCycleError reports whether the failure is a compilation or test failure
// (a normal edit-build-test loop), which is not a CLI misuse to correct.
func isTDDCycleError(output string) bool {
	return strings.Contains(output, "error[E") ||
		strings.Contains(output, "aborting due to") ||
		strings.Contains(output, "test result: FAILED") ||
		strings.Contains(output, "tests failed") ||
		strings.Contains(output, "FAIL\t")
}

// baseCommand returns the leading 1-2 significant tokens of a command (the
// program and its subcommand), after dropping leading NAME=value env
// assignments. It is the grouping key: corrections only pair commands that share
// a base.
func baseCommand(cmd string) string {
	toks := strings.Fields(strings.TrimSpace(cmd))
	for len(toks) > 0 && isEnvAssignment(toks[0]) {
		toks = toks[1:]
	}
	switch len(toks) {
	case 0:
		return ""
	case 1:
		return toks[0]
	default:
		return toks[0] + " " + toks[1]
	}
}

func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range tok[:eq] {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// commandSimilarity scores how alike two commands are: 0 when their base
// commands differ, otherwise 0.5 for the shared base plus up to 0.5 from the
// Jaccard overlap of their argument sets. Identical commands score 1.0.
func commandSimilarity(a, b string) float64 {
	ba, bb := baseCommand(a), baseCommand(b)
	if ba == "" || ba != bb {
		return 0
	}
	argsA := argSet(strings.TrimPrefix(strings.TrimSpace(a), ba))
	argsB := argSet(strings.TrimPrefix(strings.TrimSpace(b), bb))
	if len(argsA) == 0 && len(argsB) == 0 {
		return 1.0
	}
	inter := 0
	for tok := range argsA {
		if argsB[tok] {
			inter++
		}
	}
	union := len(argsA)
	for tok := range argsB {
		if !argsA[tok] {
			union++
		}
	}
	if union == 0 {
		return 0.5
	}
	return 0.5 + float64(inter)/float64(union)*0.5
}

func argSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, tok := range strings.Fields(s) {
		set[tok] = true
	}
	return set
}

// differsOnlyByPath reports whether two commands are near-identical (similarity
// above 0.9 but not equal), which usually means a path/operand changed during
// exploration rather than a genuine correction.
func differsOnlyByPath(a, b string) bool {
	s := commandSimilarity(a, b)
	return s > 0.9 && s < 1.0
}

// CorrectionPair is one detected "ran X (failed) then ran Y (worked)" event.
type CorrectionPair struct {
	Wrong      string
	Right      string
	Kind       ErrorKind
	Confidence float64
	Example    string // a short, scrubbed slice of the error output
}

// findCorrections scans one ordered session for failure->fix pairs. For each
// genuine CLI error it looks ahead a small window for a similar, non-identical
// command, boosting confidence when that command succeeded.
func findCorrections(session []Execution) []CorrectionPair {
	var pairs []CorrectionPair
	for i := range session {
		cmd := session[i]
		if !isCommandError(cmd) || !isSimpleCommand(cmd.Command) {
			continue
		}
		if isTDDCycleError(cmd.Output) {
			continue
		}
		kind := classifyError(cmd.Output)
		// Only learn from recognized CLI-misuse errors. A "general error" is almost
		// always a compile/test/runtime failure (a logic bug or TDD loop), not a
		// command the user typed wrong, so pairing it would manufacture noise.
		if kind == ErrGeneral {
			continue
		}
		for j := i + 1; j < len(session) && j <= i+CorrectionWindow; j++ {
			cand := session[j]
			if !isSimpleCommand(cand.Command) {
				continue
			}
			sim := commandSimilarity(cmd.Command, cand.Command)
			if sim < 0.5 || cmd.Command == cand.Command || differsOnlyByPath(cmd.Command, cand.Command) {
				continue
			}
			confidence := sim
			if !isCommandError(cand) {
				confidence = min1(confidence + 0.2)
			}
			if confidence < MinConfidence {
				continue
			}
			pairs = append(pairs, CorrectionPair{
				Wrong:      cmd.Command,
				Right:      cand.Command,
				Kind:       kind,
				Confidence: confidence,
				Example:    snippet(cmd.Output, 200),
			})
			break // first plausible fix only
		}
	}
	return pairs
}

func min1(v float64) float64 {
	if v > 1.0 {
		return 1.0
	}
	return v
}

func snippet(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// CorrectionRule aggregates correction pairs that share a base command and the
// same wrong->right token change, with an occurrence count.
type CorrectionRule struct {
	Base        string
	Wrong       string
	Right       string
	Diff        string // e.g. "--colour -> --color"
	Kind        ErrorKind
	Occurrences int
	Confidence  float64 // best confidence seen
	Example     string
}

// aggregate folds correction pairs into deduplicated rules, keyed by base
// command and the distinctive token change, keeping the highest confidence and a
// representative example. Rules below minOccurrences are dropped. The result is
// sorted by occurrences then confidence, both descending.
func aggregate(pairs []CorrectionPair, minOccurrences int) []CorrectionRule {
	if minOccurrences < 1 {
		minOccurrences = 1
	}
	idx := map[string]*CorrectionRule{}
	var order []string
	for _, p := range pairs {
		diff := diffTokens(p.Wrong, p.Right)
		base := baseCommand(p.Wrong)
		key := base + "\x00" + diff
		r := idx[key]
		if r == nil {
			r = &CorrectionRule{
				Base: base, Wrong: p.Wrong, Right: p.Right,
				Diff: diff, Kind: p.Kind, Example: p.Example,
			}
			idx[key] = r
			order = append(order, key)
		}
		r.Occurrences++
		if p.Confidence > r.Confidence {
			r.Confidence = p.Confidence
		}
	}
	var rules []CorrectionRule
	for _, k := range order {
		if idx[k].Occurrences >= minOccurrences {
			rules = append(rules, *idx[k])
		}
	}
	sortRules(rules)
	return rules
}

// diffTokens summarizes the distinctive change between a wrong and right
// command: the first removed token mapped to the first added token (or whichever
// side changed). It powers rule grouping and the human-readable hint.
func diffTokens(wrong, right string) string {
	w := strings.Fields(wrong)
	r := strings.Fields(right)
	wset := map[string]bool{}
	for _, t := range w {
		wset[t] = true
	}
	rset := map[string]bool{}
	for _, t := range r {
		rset[t] = true
	}
	var removed, added []string
	for _, t := range w {
		if !rset[t] {
			removed = append(removed, t)
		}
	}
	for _, t := range r {
		if !wset[t] {
			added = append(added, t)
		}
	}
	switch {
	case len(removed) > 0 && len(added) > 0:
		return removed[0] + " -> " + added[0]
	case len(removed) > 0:
		return "removed " + removed[0]
	case len(added) > 0:
		return "added " + added[0]
	default:
		return ""
	}
}

func sortRules(rules []CorrectionRule) {
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0; j-- {
			a, b := rules[j-1], rules[j]
			less := a.Occurrences < b.Occurrences ||
				(a.Occurrences == b.Occurrences && a.Confidence < b.Confidence)
			if !less {
				break
			}
			rules[j-1], rules[j] = rules[j], rules[j-1]
		}
	}
}
