package filter

import "testing"

func TestSqueezeBlankLines(t *testing.T) {
	in := "a  \n\n\n\nb\n   \nc\n"
	// trailing ws trimmed; runs of blank lines collapse to one.
	want := "a\n\nb\n\nc\n"
	if got := squeezeBlankLines(in); got != want {
		t.Errorf("squeezeBlankLines\n got  %q\n want %q", got, want)
	}
}

func TestUltraCompactGate(t *testing.T) {
	cf := firstFilter(t, "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\n")
	input := "line1\n\n\n\nline2"

	SetUltraCompact(false)
	t.Cleanup(func() { SetUltraCompact(false) })
	if got := Apply(cf, input); got != input {
		t.Errorf("ultra-compact off should pass through, got %q", got)
	}

	SetUltraCompact(true)
	if got := Apply(cf, input); got != "line1\n\nline2" {
		t.Errorf("ultra-compact on should squeeze blanks, got %q", got)
	}
}
