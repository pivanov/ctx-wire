package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/draft"
	"ctx-wire/internal/filter"
)

func mkDraft(t *testing.T, program, command, sample string) (draft.Spec, string) {
	t.Helper()
	spec := draft.Infer(program, command, sample, "2026-06-07T10:00:00Z")
	cf, err := filter.CompileDraft(spec.Name, spec.FilterSpec())
	if err != nil {
		t.Fatal(err)
	}
	return spec, filter.Apply(cf, sample)
}

// TestWriteLocalDraftCollision is the must-have: drafting the same program twice
// must not corrupt the file (no duplicate [filters.<name>] table).
func TestWriteLocalDraftCollision(t *testing.T) {
	t.Chdir(t.TempDir())

	var sb strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
		if i%10 == 0 {
			sb.WriteString("\n")
		}
	}
	sample := sb.String()
	spec, expected := mkDraft(t, "demo", "demo run", sample)
	theme := themeForStdout()

	if code := writeLocalDraft(theme, spec, sample, expected); code != 0 {
		t.Fatalf("first write exit %d, want 0", code)
	}
	wd, _ := os.Getwd()
	path := filter.ProjectFiltersPath(wd)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if code := writeLocalDraft(theme, spec, sample, expected); code != 1 {
		t.Errorf("second write exit %d, want 1 (collision must be refused)", code)
	}
	after, _ := os.ReadFile(path)
	if string(first) != string(after) {
		t.Error("a refused collision must not modify the file")
	}
	if _, err := filter.VerifyFile(path, ""); err != nil {
		t.Errorf("file no longer parses after the collision attempt: %v", err)
	}
}

// TestWriteLocalDraftRefusesInvalidExisting pins that an existing but invalid
// .ctx-wire/filters.toml (here: missing schema_version) is reported and left
// untouched, not appended-to-and-then-failed.
func TestWriteLocalDraftRefusesInvalidExisting(t *testing.T) {
	t.Chdir(t.TempDir())
	wd, _ := os.Getwd()
	path := filter.ProjectFiltersPath(wd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const bad = "[filters.x]\nmatch_command = \"^x\\\\b\"\n" // no schema_version
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, expected := mkDraft(t, "demo", "demo run", "a\n\nb\n")
	if code := writeLocalDraft(themeForStdout(), spec, "a\n\nb\n", expected); code != 1 {
		t.Errorf("exit %d, want 1 (must refuse an invalid existing file)", code)
	}
	after, _ := os.ReadFile(path)
	if string(after) != bad {
		t.Error("an invalid existing file must not be modified")
	}
}

// TestWriteLocalDraftAppends pins that a second, different draft appends without
// a duplicate schema_version header and still verifies.
func TestWriteLocalDraftAppends(t *testing.T) {
	t.Chdir(t.TempDir())
	theme := themeForStdout()

	s1, e1 := mkDraft(t, "demo", "demo run", "a\n\nb\n\nc\n")
	if code := writeLocalDraft(theme, s1, "a\n\nb\n\nc\n", e1); code != 0 {
		t.Fatalf("write 1 exit %d", code)
	}
	s2, e2 := mkDraft(t, "other", "other go", "x\n\ny\n")
	if code := writeLocalDraft(theme, s2, "x\n\ny\n", e2); code != 0 {
		t.Fatalf("write 2 (append) exit %d", code)
	}

	wd, _ := os.Getwd()
	data, err := os.ReadFile(filter.ProjectFiltersPath(wd))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "schema_version"); n != 1 {
		t.Errorf("want exactly one schema_version after append, got %d", n)
	}
	res, err := filter.VerifyFile(filter.ProjectFiltersPath(wd), "")
	if err != nil {
		t.Fatalf("merged file does not parse: %v", err)
	}
	if len(res.Outcomes) != 2 {
		t.Errorf("want 2 test outcomes across both filters, got %d", len(res.Outcomes))
	}
}
