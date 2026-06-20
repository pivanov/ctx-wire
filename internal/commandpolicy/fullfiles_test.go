package commandpolicy

import "testing"

func TestIsFullFileRead(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"cat", []string{"SKILL.md"}, true},
		{"cat", []string{"/repo/.claude/skills/foo/SKILL.md"}, true}, // basename is SKILL.md
		{"nl", []string{"-ba", "AGENTS.md"}, true},
		{"cat", []string{"CLAUDE.md"}, true},
		{"bat", []string{"SKILL.md"}, true},
		{"cat", []string{"SKILL.md", "AGENTS.md"}, true}, // all operands are full-files
		{"cat", []string{"SKILL.md", "huge.log"}, false}, // mixed: must NOT uncap (context-flood guard)
		{"cat", []string{"huge.log", "SKILL.md"}, false}, // order does not matter
		{"cat", []string{"README.md"}, false},
		{"cat", []string{"data.json"}, false},
		{"head", []string{"SKILL.md"}, false}, // head is a partial read by intent
		{"tail", []string{"SKILL.md"}, false},
		{"rg", []string{"foo", "SKILL.md"}, false}, // not a full-content reader
		{"cat", []string{"-n"}, false},             // only a flag, no file
		{"cat", nil, false},
	}
	for _, c := range cases {
		if got := IsFullFileRead(c.name, c.args); got != c.want {
			t.Errorf("IsFullFileRead(%q, %v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}

func TestSetFullFilesExtendsDefaults(t *testing.T) {
	t.Cleanup(func() { SetFullFiles(nil) }) // restore the built-in defaults
	SetFullFiles([]string{"*.skill", "  ", "PLAYBOOK.md"})
	if !IsFullFileRead("cat", []string{"my.skill"}) {
		t.Error("configured *.skill glob should match")
	}
	if !IsFullFileRead("cat", []string{"PLAYBOOK.md"}) {
		t.Error("configured PLAYBOOK.md should match")
	}
	if !IsFullFileRead("cat", []string{"SKILL.md"}) {
		t.Error("built-in defaults must still apply after SetFullFiles")
	}
}
