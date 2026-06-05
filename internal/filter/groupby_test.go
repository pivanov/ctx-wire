package filter

import (
	"strings"
	"testing"
)

// groupByTOML builds a filter with a group_by stage plus optional extra fields.
func groupByTOML(extra string) string {
	return `schema_version = 1
[filters.f]
match_command = "^cmd"
group_by = { key = "^([^:]+):", max_per_group = 2, max_groups = 2, omit_label = "... +%d more matches in %s" }
` + extra
}

func firstCompiled(t *testing.T, content string) *CompiledFilter {
	t.Helper()
	fs, err := parseAndCompile(content, "test")
	if err != nil {
		t.Fatalf("parseAndCompile: %v", err)
	}
	if len(fs) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(fs))
	}
	return fs[0]
}

func TestGroupByShortOutputUnchanged(t *testing.T) {
	f := firstCompiled(t, groupByTOML(""))
	in := "a.go:1:x\nb.go:2:y" // 2 groups, 1 line each: under all caps
	if got := Apply(f, in); got != in {
		t.Errorf("short output changed:\n got:  %q\n want: %q", got, in)
	}
}

func TestGroupByPerGroupCap(t *testing.T) {
	f := firstCompiled(t, groupByTOML(""))
	in := "a.go:1:x\na.go:2:y\na.go:3:z\na.go:4:w"
	want := "a.go:1:x\na.go:2:y\n... +2 more matches in a.go"
	if got := Apply(f, in); got != want {
		t.Errorf("per-group cap:\n got:  %q\n want: %q", got, want)
	}
}

func TestGroupByMaxGroupsCap(t *testing.T) {
	f := firstCompiled(t, groupByTOML(""))
	// 3 groups, max_groups = 2: third group dropped, summarized.
	in := "a.go:1:x\nb.go:1:y\nc.go:1:z"
	got := Apply(f, in)
	if !strings.Contains(got, "a.go:1:x") || !strings.Contains(got, "b.go:1:y") {
		t.Errorf("kept groups missing: %q", got)
	}
	if strings.Contains(got, "c.go:1:z") {
		t.Errorf("third group should be dropped: %q", got)
	}
	if !strings.Contains(got, "+1 more groups") {
		t.Errorf("missing dropped-groups note: %q", got)
	}
}

func TestGroupByUnmatchedLinesPassThrough(t *testing.T) {
	f := firstCompiled(t, groupByTOML(""))
	// No "key:" lines at all (e.g. rg -l): untouched.
	in := "src/a.go\nsrc/b.go\nsrc/c.go"
	if got := Apply(f, in); got != in {
		t.Errorf("unmatched output changed:\n got:  %q\n want: %q", got, in)
	}
}

func TestGroupByPreservesInterleavedNonMatchLines(t *testing.T) {
	f := firstCompiled(t, groupByTOML(""))
	in := "Binary file x matches\na.go:1:x\na.go:2:y\na.go:3:z"
	want := "Binary file x matches\na.go:1:x\na.go:2:y\n... +1 more matches in a.go"
	if got := Apply(f, in); got != want {
		t.Errorf("interleaved non-match line:\n got:  %q\n want: %q", got, want)
	}
}

func TestGroupByThenMaxLines(t *testing.T) {
	// max_lines applies after group_by: grouping produces 4 group lines + caps.
	f := firstCompiled(t, groupByTOML("max_lines = 3\n"))
	in := "a.go:1:x\na.go:2:y\nb.go:1:z\nb.go:2:w"
	got := Apply(f, in)
	if !strings.Contains(got, "lines truncated") {
		t.Errorf("max_lines should still cap after group_by: %q", got)
	}
	if n := len(strings.Split(got, "\n")); n != 4 { // 3 kept + truncated note
		t.Errorf("max_lines=3 should yield 4 lines, got %d: %q", n, got)
	}
}

func TestGroupByInvalidConfigFailsCompile(t *testing.T) {
	tests := []struct {
		name string
		def  string
	}{
		{"missing key", `group_by = { max_per_group = 2, max_groups = 2, omit_label = "x %d %s" }`},
		{"key without capture group", `group_by = { key = "^[^:]+:", max_per_group = 2, max_groups = 2, omit_label = "x %d %s" }`},
		{"invalid key regex", `group_by = { key = "^([", max_per_group = 2, max_groups = 2, omit_label = "x %d %s" }`},
		{"zero max_per_group", `group_by = { key = "^([^:]+):", max_per_group = 0, max_groups = 2, omit_label = "x %d %s" }`},
		{"zero max_groups", `group_by = { key = "^([^:]+):", max_per_group = 2, max_groups = 0, omit_label = "x %d %s" }`},
		{"missing omit_label", `group_by = { key = "^([^:]+):", max_per_group = 2, max_groups = 2 }`},
		{"bad omit_label verbs", `group_by = { key = "^([^:]+):", max_per_group = 2, max_groups = 2, omit_label = "no verbs here" }`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			content := "schema_version = 1\n[filters.f]\nmatch_command = \"^cmd\"\n" + tt.def + "\n"
			fs, err := parseAndCompile(content, "test")
			// parseAndCompile logs a warning and skips a bad filter, yielding an
			// empty set (verify surfaces the same as a failure to load it).
			if err == nil && len(fs) != 0 {
				t.Errorf("expected invalid group_by to be rejected, got %d filters", len(fs))
			}
		})
	}
}
