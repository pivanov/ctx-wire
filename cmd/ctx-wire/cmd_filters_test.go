package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/filter"
)

func writeReg(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFiltersPullLocalAndCollision(t *testing.T) {
	reg := t.TempDir()
	writeReg(t, reg, "demo", `schema_version = 1

[filters.demo]
description = "demo"
match_command = "^demo\\b"
strip_lines_matching = ["^\\s*$"]

[[tests.demo]]
name = "strips blanks"
input = "a\n\nb"
expected = "a\nb"
`)

	t.Chdir(t.TempDir())
	if code := cmdFiltersPull([]string{"demo", "--registry", reg}); code != 0 {
		t.Fatalf("pull exit %d, want 0", code)
	}
	wd, _ := os.Getwd()
	path := filter.ProjectFiltersPath(wd)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[filters.demo]") {
		t.Error("pulled filter was not installed")
	}
	if _, err := filter.VerifyFile(path, ""); err != nil {
		t.Errorf("installed file does not verify: %v", err)
	}
	// A second pull of the same name must refuse (collision), not corrupt the file.
	if code := cmdFiltersPull([]string{"demo", "--registry", reg}); code != 1 {
		t.Errorf("collision pull exit %d, want 1", code)
	}
}

func TestFiltersPullRejectsUnsafe(t *testing.T) {
	reg := t.TempDir()
	// Failing inline test.
	writeReg(t, reg, "bad", `schema_version = 1
[filters.bad]
match_command = "^bad\\b"
strip_lines_matching = ["^\\s*$"]
[[tests.bad]]
name = "wrong"
input = "a\n\nb"
expected = "WRONG"
`)
	// Unfinished draft marker.
	writeReg(t, reg, "drafty", `schema_version = 1
[filters.drafty]
match_command = "^drafty\\b"
[[tests.drafty]]
name = "draft"
draft = true
input = "a"
expected = "a"
`)

	t.Chdir(t.TempDir())
	if code := cmdFiltersPull([]string{"bad", "--registry", reg}); code != 1 {
		t.Errorf("a filter with a failing test must be refused, got exit %d", code)
	}
	if code := cmdFiltersPull([]string{"drafty", "--registry", reg}); code != 1 {
		t.Errorf("a filter shipping a draft marker must be refused, got exit %d", code)
	}
	if code := cmdFiltersPull([]string{"missing", "--registry", reg}); code != 1 {
		t.Errorf("a missing filter must error, got exit %d", code)
	}
	// Nothing should have been written.
	wd, _ := os.Getwd()
	if _, err := os.Stat(filter.ProjectFiltersPath(wd)); err == nil {
		t.Error("refused pulls must not write a project filter file")
	}
}

func TestFiltersPullRejectsNameMismatch(t *testing.T) {
	reg := t.TempDir()
	// Fetched as "mytool" but defines a different filter key: a shadow attempt
	// (after trust, a project filter named git-status would shadow the built-in).
	writeReg(t, reg, "mytool", `schema_version = 1
[filters.git-status]
match_command = "^git status\\b"
[[tests.git-status]]
name = "t"
input = "x"
expected = "x"
`)
	t.Chdir(t.TempDir())
	if code := cmdFiltersPull([]string{"mytool", "--registry", reg}); code != 1 {
		t.Errorf("pull must refuse a file whose filter key != requested name, got exit %d", code)
	}
	wd, _ := os.Getwd()
	if _, err := os.Stat(filter.ProjectFiltersPath(wd)); err == nil {
		t.Error("a mismatched pull must not write anything")
	}
}

func TestStripSchemaVersionExact(t *testing.T) {
	if !isSchemaVersionAssignment("schema_version = 1") || !isSchemaVersionAssignment("  schema_version=2") {
		t.Error("a real schema_version assignment should match")
	}
	if isSchemaVersionAssignment("schema_version_extra = 1") {
		t.Error("a different key starting with schema_version must not match")
	}
	out := stripSchemaVersion("schema_version = 1\nschema_version_extra = 9\n[filters.x]\n")
	if strings.Contains(out, "schema_version = 1") {
		t.Error("stripSchemaVersion should drop the real line")
	}
	if !strings.Contains(out, "schema_version_extra") {
		t.Error("stripSchemaVersion must not drop an unrelated key")
	}
}

func TestFilterHelpers(t *testing.T) {
	content := "schema_version = 1\n\n[filters.a]\nmatch_command = \"^a\"\n\n[[tests.a]]\nname=\"t\"\ninput=\"a\"\nexpected=\"a\"\n"
	if names := filterNames(content); len(names) != 1 || names[0] != "a" {
		t.Errorf("filterNames = %v, want [a]", names)
	}
	if s := stripSchemaVersion(content); strings.Contains(s, "schema_version") {
		t.Error("stripSchemaVersion left the line in")
	}
	block, ok := extractFilterBlock(content, "a")
	if !ok || !strings.Contains(block, "[filters.a]") || !strings.Contains(block, "[[tests.a]]") {
		t.Errorf("extractFilterBlock = %q ok=%v", block, ok)
	}
}
