package main

import (
	"os"
	"path/filepath"
	"testing"
)

const draftFiltersFile = `schema_version = 1

[filters.demo]
description = "demo"
match_command = "^demo\\b"
strip_lines_matching = ["^\\s*$"]

[[tests.demo]]
name = "draft from claude transcript"
draft = true
input = "a\n\nb"
expected = "a\nb"
`

// TestCmdVerifyDraftExitCodes pins the exit behavior: a lingering draft test
// fails verification, --allow-draft lets a local file through.
func TestCmdVerifyDraftExitCodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filters.toml")
	if err := os.WriteFile(path, []byte(draftFiltersFile), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := cmdVerify([]string{"--file", path}); code != 1 {
		t.Errorf("draft file: exit %d, want 1 (draft must fail verification)", code)
	}
	if code := cmdVerify([]string{"--file", path, "--allow-draft"}); code != 0 {
		t.Errorf("draft file with --allow-draft: exit %d, want 0", code)
	}
}
