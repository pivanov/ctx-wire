package filter

import "testing"

// On failure the runner sets KeepTailOnTruncate so the absolute max_lines cap
// keeps the end of the output (where test summaries and the failing assertion
// live) instead of the head.
func TestMaxLinesKeepTailOnTruncate(t *testing.T) {
	cf := firstFilter(t, "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nmax_lines=3\n")
	input := "a\nb\nc\nd\nFAIL: boom"

	head := ApplyWithMetaOptions(cf, input, ApplyOptions{}).Output
	if want := "a\nb\nc\n... (2 lines truncated)"; head != want {
		t.Errorf("head-keep (success)\n got  %q\n want %q", head, want)
	}

	tail := ApplyWithMetaOptions(cf, input, ApplyOptions{KeepTailOnTruncate: true}).Output
	if want := "... (2 lines truncated)\nc\nd\nFAIL: boom"; tail != want {
		t.Errorf("tail-keep (failure)\n got  %q\n want %q", tail, want)
	}
	// The whole point: the failing line survives the cap.
	if !containsSubstr(tail, "FAIL: boom") {
		t.Errorf("failing line must survive truncation, got %q", tail)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
