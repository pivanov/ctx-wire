package filter

import (
	"strings"
	"testing"
)

// TestBuiltinHasNoUnknownFields guards that every built-in filter uses only
// recognized TOML fields; an unknown field now fails LoadBuiltin.
func TestBuiltinHasNoUnknownFields(t *testing.T) {
	if _, err := LoadBuiltin(); err != nil {
		t.Fatalf("built-in filters must have no unrecognized fields: %v", err)
	}
}

// TestParseRejectsUnknownBuiltinField proves a typo'd field in a built-in filter
// is a hard error (not silently ignored).
func TestParseRejectsUnknownBuiltinField(t *testing.T) {
	content := "schema_version = 1\n[filters.demo]\nmatch_command = \"^demo\"\nstrip_line_matching = [\"x\"]\n"
	_, err := parseAndCompile(content, "builtin")
	if err == nil {
		t.Fatal("expected an unknown-field error for a built-in typo, got nil")
	}
	if !strings.Contains(err.Error(), "strip_line_matching") {
		t.Fatalf("error should name the offending field, got: %v", err)
	}
}

// TestParseWarnsButKeepsUserFilterWithUnknownField proves project/user filters
// stay fail-open: an unknown field is warned about, not fatal, and the
// recognized fields still compile.
func TestParseWarnsButKeepsUserFilterWithUnknownField(t *testing.T) {
	content := "schema_version = 1\n[filters.demo]\nmatch_command = \"^demo\"\nmax_lines = 5\nbogus_field = true\n"
	cfs, err := parseAndCompile(content, "user")
	if err != nil {
		t.Fatalf("user filter with an unknown field must not error (fail-open): %v", err)
	}
	if len(cfs) != 1 || cfs[0].Name != "demo" {
		t.Fatalf("expected the demo filter to still compile, got %+v", cfs)
	}
}
