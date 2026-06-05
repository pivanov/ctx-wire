package commandpolicy

import "testing"

func TestTransparentPrefix(t *testing.T) {
	t.Cleanup(func() { SetTransparentPrefixes(nil) })
	SetTransparentPrefixes([]string{"docker exec web", " direnv exec . ", ""})

	if i, ok := TransparentPrefix("docker exec web git status"); !ok || i != len("docker exec web ") {
		t.Errorf("docker prefix: got (%d, %v), want (%d, true)", i, ok, len("docker exec web "))
	}
	if i, ok := TransparentPrefix("direnv exec . npm test"); !ok || i != len("direnv exec . ") {
		t.Errorf("direnv prefix (trimmed): got (%d, %v)", i, ok)
	}
	// No whole-token boundary: "docker exec website" must not match "docker exec web".
	if _, ok := TransparentPrefix("docker exec website ls"); ok {
		t.Error("partial token should not match")
	}
	// Prefix with no inner command does not match.
	if _, ok := TransparentPrefix("docker exec web"); ok {
		t.Error("prefix-only (no inner) should not match")
	}
	// Unrelated command.
	if _, ok := TransparentPrefix("git status"); ok {
		t.Error("non-prefixed command should not match")
	}

	SetTransparentPrefixes(nil)
	if _, ok := TransparentPrefix("docker exec web git status"); ok {
		t.Error("clearing prefixes should disable matching")
	}
}
