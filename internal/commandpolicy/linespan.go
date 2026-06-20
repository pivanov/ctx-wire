package commandpolicy

import (
	"path/filepath"
	"strconv"
	"strings"
)

// ExplicitLineSpan reports the number of output lines a command explicitly,
// concretely, and BOUNDEDLY requests. A runner uses it to lift the per-filter
// line cap to the agent's own bound, so a deliberate slice (e.g. paging a doc
// with sed -n 'A,Bp') is not re-capped. It returns ok=false for anything that is
// not an unambiguous bounded numeric count/range, so the cap stays as a guard.
// Deliberately conservative: when in doubt, keep the cap.
//
//	Honored:  sed -n 'A,Bp', sed -n 'A,+Np', sed -n 'Ap'; head -n N, tail -n N
//	          (and -nN, --lines N, --lines=N, old -N).
//	Rejected: tail -n +N (to EOF), head -n -N (all but last N), any $ endpoint,
//	          regex addresses, multi-command scripts, byte mode (-c), script files.
func ExplicitLineSpan(name string, args []string) (span int, ok bool) {
	switch filepath.Base(name) {
	case "sed":
		return sedLineSpan(args)
	case "head", "tail":
		return headTailLineCount(args)
	}
	return 0, false
}

// headTailLineCount parses an explicit, bounded line count from head/tail args.
// It rejects byte mode and the unbounded +N / -N value forms.
func headTailLineCount(args []string) (int, bool) {
	val := ""
	found := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" || a == "--bytes" || strings.HasPrefix(a, "-c") || strings.HasPrefix(a, "--bytes="):
			return 0, false // byte mode, not a line count
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return 0, false
			}
			val, found = args[i+1], true
			i++
		case strings.HasPrefix(a, "--lines="):
			val, found = strings.TrimPrefix(a, "--lines="), true
		case strings.HasPrefix(a, "-n"):
			val, found = a[2:], true // -nN
		case len(a) >= 2 && a[0] == '-' && a[1] >= '0' && a[1] <= '9':
			val, found = a[1:], true // old -N form
		default:
			continue // other flag (-q, -v, -z, ...) or a file operand: irrelevant here
		}
	}
	if !found || val == "" {
		return 0, false
	}
	if val[0] == '+' || val[0] == '-' {
		return 0, false // tail -n +N (to EOF) and head -n -N (all but last) are unbounded
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// sedLineSpan honors only the simple, common, bounded slice: sed -n 'SCRIPT'
// where SCRIPT is a single numeric line address or an A,B / A,+N range ending in p.
func sedLineSpan(args []string) (int, bool) {
	var scripts []string
	hasN := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-n":
			hasN = true
		case a == "-e" || a == "--expression":
			if i+1 >= len(args) {
				return 0, false
			}
			scripts = append(scripts, args[i+1])
			i++
		case strings.HasPrefix(a, "-e"):
			scripts = append(scripts, a[2:])
		case a == "-f" || a == "--file" || strings.HasPrefix(a, "-f") ||
			a == "-i" || strings.HasPrefix(a, "-i") || a == "--in-place":
			return 0, false // script from file or in-place edit: not our case
		case strings.HasPrefix(a, "-"):
			continue // other flag (-E, -r, --posix, ...) is benign
		default:
			if len(scripts) == 0 {
				scripts = append(scripts, a) // first bare arg is the script
			}
			// later bare args are file operands
		}
	}
	if !hasN || len(scripts) != 1 {
		return 0, false // only `sed -n 'SINGLE SCRIPT'`
	}
	return parseSedRange(scripts[0])
}

// parseSedRange parses "A,Bp" / "A,+Np" / "Ap" into a line span, rejecting $
// endpoints, regex addresses, negation, step addresses, and multi-command scripts.
func parseSedRange(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, ";{}") { // multiple commands or a block
		return 0, false
	}
	if !strings.HasSuffix(s, "p") {
		return 0, false // only a bare print command
	}
	body := strings.TrimSpace(strings.TrimSuffix(s, "p"))
	if body == "" {
		return 0, false // bare 'p' prints every line: unbounded
	}
	if strings.ContainsAny(body, "/$!~") { // regex addr, last-line, negation, step
		return 0, false
	}
	if !strings.Contains(body, ",") {
		if a, err := strconv.Atoi(body); err == nil && a >= 1 {
			return 1, true // single line address: Ap
		}
		return 0, false
	}
	parts := strings.SplitN(body, ",", 2)
	a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || a < 1 {
		return 0, false
	}
	end := strings.TrimSpace(parts[1])
	if strings.HasPrefix(end, "+") { // A,+N -> N lines after A
		if n, err := strconv.Atoi(end[1:]); err == nil && n >= 0 {
			return n + 1, true
		}
		return 0, false
	}
	b, err := strconv.Atoi(end)
	if err != nil || b < a {
		return 0, false
	}
	return b - a + 1, true
}
