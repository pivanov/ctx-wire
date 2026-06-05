package learn

import "testing"

func TestBaseCommand(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git status", "git status"},
		{"git log --oneline", "git log"},
		{"FOO=bar git log --oneline", "git log"},
		{"FOO=bar BAZ=q go test ./...", "go test"},
		{"ls", "ls"},
		{"", ""},
	}
	for _, c := range cases {
		if got := baseCommand(c.in); got != c.want {
			t.Errorf("baseCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCommandSimilarity(t *testing.T) {
	if s := commandSimilarity("git log --one-line", "git log --oneline"); s < 0.49 || s > 0.51 {
		t.Errorf("flag-change similarity = %.3f, want ~0.5", s)
	}
	if s := commandSimilarity("git status", "go build"); s != 0 {
		t.Errorf("different base similarity = %.3f, want 0", s)
	}
	if s := commandSimilarity("git status", "git status"); s != 1.0 {
		t.Errorf("identical similarity = %.3f, want 1.0", s)
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		out  string
		want ErrorKind
	}{
		{"error: unexpected argument '--foo' found", ErrUnknownFlag},
		{"bash: frobnicate: command not found", ErrCommandNotFound},
		{"error: the argument '--out' requires a value", ErrMissingArg},
		{"open x: permission denied", ErrPermissionDenied},
		{"cat: y: no such file or directory", ErrWrongPath},
		{"something else entirely went wrong", ErrGeneral},
	}
	for _, c := range cases {
		if got := classifyError(c.out); got != c.want {
			t.Errorf("classifyError(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

func TestIsCommandError(t *testing.T) {
	if isCommandError(Execution{IsError: false, Output: "error: bad"}) {
		t.Error("non-error result should not count even with error text")
	}
	if isCommandError(Execution{IsError: true, Output: "user declined to run this command"}) {
		t.Error("user rejection should not count as a CLI error")
	}
	if isCommandError(Execution{IsError: true, Output: "everything is fine"}) {
		t.Error("error flag without error markers should not count")
	}
	if !isCommandError(Execution{IsError: true, Output: "error: unknown flag --foo"}) {
		t.Error("genuine error should count")
	}
}

func TestFindCorrectionsFlagFix(t *testing.T) {
	session := []Execution{
		{Command: "git log --one-line", IsError: true, Output: "error: unknown option '--one-line'"},
		{Command: "git log --oneline", IsError: false, Output: "abc123 commit"},
	}
	pairs := findCorrections(session)
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want 1: %+v", len(pairs), pairs)
	}
	p := pairs[0]
	if p.Wrong != "git log --one-line" || p.Right != "git log --oneline" {
		t.Errorf("pair = %+v", p)
	}
	if p.Kind != ErrUnknownFlag {
		t.Errorf("kind = %q, want unknown flag", p.Kind)
	}
	if p.Confidence < 0.6 {
		t.Errorf("confidence = %.3f, want >= 0.6 (success boost)", p.Confidence)
	}
}

func TestFindCorrectionsSkipsTDDAndIdentical(t *testing.T) {
	// Compilation failure then a re-run is a TDD loop, not a CLI correction.
	tdd := []Execution{
		{Command: "go test ./...", IsError: true, Output: "FAIL\tctx-wire/foo\nerror running tests"},
		{Command: "go test ./...", IsError: false, Output: "ok"},
	}
	if got := findCorrections(tdd); len(got) != 0 {
		t.Errorf("TDD/identical re-run should yield no corrections, got %+v", got)
	}
}

func TestAggregateDedupAndMinOccurrences(t *testing.T) {
	pairs := []CorrectionPair{
		{Wrong: "git log --one-line", Right: "git log --oneline", Kind: ErrUnknownFlag, Confidence: 0.7},
		{Wrong: "git log --one-line", Right: "git log --oneline", Kind: ErrUnknownFlag, Confidence: 0.8},
		{Wrong: "go buld", Right: "go build", Kind: ErrCommandNotFound, Confidence: 0.7},
	}
	rules := aggregate(pairs, 1)
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2: %+v", len(rules), rules)
	}
	// The git rule has 2 occurrences and should sort first.
	if rules[0].Occurrences != 2 || rules[0].Base != "git log" {
		t.Errorf("top rule = %+v, want git log x2", rules[0])
	}
	if rules[0].Confidence != 0.8 {
		t.Errorf("rule confidence = %.2f, want best 0.8", rules[0].Confidence)
	}
	// minOccurrences=2 drops the single go-build correction.
	if got := aggregate(pairs, 2); len(got) != 1 {
		t.Errorf("minOccurrences=2 should leave 1 rule, got %d", len(got))
	}
}

func TestDiffTokens(t *testing.T) {
	if d := diffTokens("git log --one-line", "git log --oneline"); d != "--one-line -> --oneline" {
		t.Errorf("diff = %q", d)
	}
	if d := diffTokens("rg --colour TODO", "rg TODO"); d != "removed --colour" {
		t.Errorf("diff = %q", d)
	}
}
