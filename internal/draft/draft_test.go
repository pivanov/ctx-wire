package draft

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/transcript"
)

func TestInferTransforms(t *testing.T) {
	// Noisy: blank lines, > manyLinesThreshold lines, one long line.
	var sb strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
		if i%10 == 0 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString(strings.Repeat("x", 300) + "\n")
	s := Infer("demo", "demo run", sb.String(), "2026-06-07T10:00:00Z")

	if len(s.StripLines) == 0 {
		t.Error("expected a blank-line strip pattern")
	}
	if s.MaxLines == nil {
		t.Error("expected max_lines on a long sample")
	}
	if s.TruncateAt == nil {
		t.Error("expected truncate_lines_at on a wide sample")
	}
	if s.IsJSON {
		t.Error("plain text wrongly classified as JSON")
	}
}

func TestInferJSONNoTruncation(t *testing.T) {
	s := Infer("jq", "jq .", `{"a":1,"b":[1,2,3]}`, "t")
	if !s.IsJSON {
		t.Fatal("valid JSON not detected")
	}
	if s.MaxLines != nil || s.TruncateAt != nil {
		t.Error("JSON must never get a line-truncating transform")
	}
}

func TestMatchCommandAndName(t *testing.T) {
	if got := matchCommand("git", "git status -s"); got != `^git status\b` {
		t.Errorf("matchCommand = %q, want ^git status\\b", got)
	}
	if got := filterName("git", "git status -s"); got != "git-status" {
		t.Errorf("filterName = %q, want git-status", got)
	}
	// A metacharacter program name must produce a valid escaped pattern.
	if got := matchCommand("g++", "g++ -o x main.c"); got != `^g\+\+\b` {
		t.Errorf("matchCommand(g++) = %q, want ^g\\+\\+\\b", got)
	}
}

type fakeMatcher map[string]bool

func (f fakeMatcher) Find(cmd string) *filter.CompiledFilter {
	if f[cmd] {
		return &filter.CompiledFilter{}
	}
	return nil
}

func TestRawSamples(t *testing.T) {
	samples := []transcript.Exec{
		{Command: "git status", Output: "x"},       // already filtered
		{Command: "kubectl get pods", Output: "y"}, // raw
	}
	raw := RawSamples(samples, fakeMatcher{"git status": true})
	if len(raw) != 1 || raw[0].Command != "kubectl get pods" {
		t.Errorf("RawSamples kept %+v, want only the unfiltered kubectl sample", raw)
	}
	// nil matcher is a no-op.
	if got := RawSamples(samples, nil); len(got) != 2 {
		t.Errorf("nil matcher should keep all, got %d", len(got))
	}
}

func TestSelectSamplesRanksByOutput(t *testing.T) {
	execs := []transcript.Exec{
		{Command: "git status", Output: "short"},
		{Command: "git status", Output: "a much longer output sample here"},
		{Command: "ls", Output: "not git"},
		{Command: "git status", Output: ""}, // empty: skipped
	}
	got := SelectSamples(execs, "git")
	if len(got) != 2 {
		t.Fatalf("got %d git samples, want 2", len(got))
	}
	if got[0].Output != "a much longer output sample here" {
		t.Errorf("largest sample not first: %q", got[0].Output)
	}
}

// TestRenderVerifies is the integration check: a drafted spec, applied to its own
// sample, renders to TOML that actually verifies, and the test is flagged Draft.
func TestRenderVerifies(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
		if i%10 == 0 {
			sb.WriteString("\n")
		}
	}
	sample := sb.String()

	s := Infer("demo", "demo run", sample, "2026-06-07T10:00:00Z")
	cf, err := filter.CompileDraft(s.Name, s.FilterSpec())
	if err != nil {
		t.Fatalf("CompileDraft: %v", err)
	}
	expected := filter.Apply(cf, sample)
	if expected == sample {
		t.Fatal("the inferred filter changed nothing; sample was not noisy enough")
	}

	tomlStr, err := s.TOML(sample, expected, Local)
	if err != nil {
		t.Fatalf("TOML: %v", err)
	}
	if !strings.Contains(tomlStr, "schema_version = 1") {
		t.Error("local target must include schema_version = 1")
	}

	path := filepath.Join(t.TempDir(), "filters.toml")
	if err := os.WriteFile(path, []byte(tomlStr), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := filter.VerifyFile(path, "")
	if err != nil {
		t.Fatalf("VerifyFile on generated draft: %v\n%s", err, tomlStr)
	}
	if len(res.Outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(res.Outcomes))
	}
	if o := res.Outcomes[0]; !o.Passed {
		t.Errorf("generated draft test should pass mechanically; actual=%q expected=%q", o.Actual, o.Expected)
	} else if !o.Draft {
		t.Error("generated test should carry draft = true")
	}
}

func TestTOMLBuiltinOmitsSchema(t *testing.T) {
	s := Infer("demo", "demo", "a\n\nb", "t")
	out, err := s.TOML("a\n\nb", "a\nb", Builtin)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "schema_version") {
		t.Error("builtin target must omit schema_version")
	}
}

func TestHasFilterCollision(t *testing.T) {
	const content = `schema_version = 1

[filters.demo]
match_command = "^demo\\b"
`
	if !HasFilter(content, "demo") {
		t.Error("HasFilter should detect an existing [filters.demo]")
	}
	if HasFilter(content, "other") {
		t.Error("HasFilter should not match a missing filter")
	}
}
