package filter

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempFilters(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "filters.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerifyFileDraftAndSchema(t *testing.T) {
	const good = `schema_version = 1

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
	res, err := VerifyFile(writeTempFilters(t, good), "")
	if err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
	if len(res.Outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(res.Outcomes))
	}
	if o := res.Outcomes[0]; !o.Passed {
		t.Errorf("draft test should pass mechanically; actual=%q expected=%q", o.Actual, o.Expected)
	} else if !o.Draft {
		t.Error("outcome should be flagged Draft")
	}

	// A project file without schema_version would not load, so it must not
	// silently "verify clean".
	const noSchema = `[filters.demo]
match_command = "^demo\\b"
`
	if _, err := VerifyFile(writeTempFilters(t, noSchema), ""); err == nil {
		t.Error("expected a schema_version error")
	}
}
