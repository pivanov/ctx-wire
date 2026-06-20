package commandpolicy

import "testing"

func TestExplicitLineSpan(t *testing.T) {
	cases := []struct {
		name string
		args []string
		span int
		ok   bool
	}{
		// Honored: sed bounded ranges.
		{"sed", []string{"-n", "988,1140p"}, 153, true},
		{"sed", []string{"-n", "988,1140p", "file.md"}, 153, true},
		{"sed", []string{"-n", "5,5p"}, 1, true},
		{"sed", []string{"-n", "10,+4p"}, 5, true},
		{"sed", []string{"-n", "42p"}, 1, true},
		{"/usr/bin/sed", []string{"-n", "1,10p"}, 10, true}, // resolved path

		// Honored: head/tail counts.
		{"head", []string{"-n", "200", "file"}, 200, true},
		{"head", []string{"-n200"}, 200, true},
		{"head", []string{"--lines=120"}, 120, true},
		{"head", []string{"-50"}, 50, true},
		{"tail", []string{"-n", "80"}, 80, true},

		// Rejected: unbounded forms (LOAD-BEARING , these look bounded but aren't).
		{"tail", []string{"-n", "+50"}, 0, false},                     // from line 50 to EOF
		{"head", []string{"-n", "-20"}, 0, false},                     // all but the last 20
		{"sed", []string{"-n", "1,$p"}, 0, false},                     // $ endpoint
		{"sed", []string{"-n", "5,$p"}, 0, false},                     //
		{"sed", []string{"-n", "/foo/,/bar/p"}, 0, false},             // regex addresses
		{"sed", []string{"-n", "/foo/p"}, 0, false},                   //
		{"sed", []string{"-n", "1,5p;10,15p"}, 0, false},              // multi-command
		{"sed", []string{"-n", "-e", "1,5p", "-e", "9,9p"}, 0, false}, // multiple -e

		// Rejected: not a bounded numeric print slice.
		{"sed", []string{"1,5p"}, 0, false},         // no -n: prints all plus the range
		{"sed", []string{"-n", "p"}, 0, false},      // bare p: every line
		{"sed", []string{"-n", "s/a/b/"}, 0, false}, // substitute, not a slice
		{"sed", []string{"-n", "5,3p"}, 0, false},   // b < a
		{"sed", []string{"-n", "0,5p"}, 0, false},   // line 0 is invalid
		{"head", []string{"-c", "100"}, 0, false},   // byte mode
		{"head", []string{"-c100"}, 0, false},       //
		{"tail", []string{"--bytes=100"}, 0, false}, //
		{"head", []string{"file"}, 0, false},        // no count given

		// Not a target command.
		{"cat", []string{"SKILL.md"}, 0, false},
		{"grep", []string{"-n", "5,10p"}, 0, false},
	}
	for _, c := range cases {
		span, ok := ExplicitLineSpan(c.name, c.args)
		if ok != c.ok || (ok && span != c.span) {
			t.Errorf("ExplicitLineSpan(%q, %v) = (%d, %v), want (%d, %v)", c.name, c.args, span, ok, c.span, c.ok)
		}
	}
}
