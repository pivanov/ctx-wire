package filter

import "testing"

// TestNegativeLineCapsFailCompile pins that a non-positive truncate/head/tail/
// max line cap is rejected at compile time rather than panicking on the hot path
// (a negative value would drive a slice past its bounds in the head/tail/
// max_lines/truncate stages). Mirrors TestGroupByInvalidConfigFailsCompile.
func TestNegativeLineCapsFailCompile(t *testing.T) {
	tests := []struct {
		name string
		def  string
	}{
		{"negative truncate_lines_at", "truncate_lines_at = -1"},
		{"negative head_lines", "head_lines = -5"},
		{"negative tail_lines", "tail_lines = -2"},
		{"negative max_lines", "max_lines = -3"},
		{"zero max_lines", "max_lines = 0"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			content := "schema_version = 1\n[filters.f]\nmatch_command = \"^cmd\"\n" + tt.def + "\n"
			fs, err := parseAndCompile(content, "test")
			if err == nil && len(fs) != 0 {
				t.Errorf("expected invalid line cap to be rejected, got %d filters", len(fs))
			}
		})
	}
}
