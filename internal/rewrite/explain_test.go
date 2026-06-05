package rewrite

import "testing"

func TestExplainSingleSegment(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantWrapped bool
		wantReason  string // substring; "" when wrapped
	}{
		{"plain command wrapped", "git status", true, ""},
		{"full-path command wrapped", "/usr/bin/git status", true, ""},
		{"pipeline unwrappable last stage passthrough", "cat log | grep err > out.txt", false, ReasonPipeline},
		{"redirect passthrough", "echo x > file", false, ReasonRedirection},
		{"append redirect passthrough", "echo x >> file", false, ReasonRedirection},
		{"shell builtin passthrough", "cd /tmp", false, ReasonShellBuiltin},
		{"assignment-only passthrough", "FOO=bar", false, ReasonEnvAssignment},
		{"already ctx-wire passthrough", "ctx-wire run git status", false, ReasonAlreadyCtxWire},
		{"local ctx-wire passthrough", "./ctx-wire gain", false, ReasonAlreadyCtxWire},
		{"empty passthrough", "", false, ReasonEmpty},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			le := Explain(tt.line)
			if len(le.Segments) != 1 {
				t.Fatalf("expected 1 segment, got %d: %+v", len(le.Segments), le.Segments)
			}
			s := le.Segments[0]
			if s.Wrapped != tt.wantWrapped {
				t.Errorf("Wrapped = %v, want %v", s.Wrapped, tt.wantWrapped)
			}
			if tt.wantWrapped {
				if le.Result != "ctx-wire run "+tt.line {
					t.Errorf("Result = %q, want wrapped", le.Result)
				}
			} else if s.Reason == "" || !contains(s.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want substring %q", s.Reason, tt.wantReason)
			}
		})
	}
}

func TestExplainPrefixAndGrouping(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantWrapped bool
		wantReason  string // substring when not wrapped
		wantInner   string // when wrapped
	}{
		{"env prefix wraps inner", "FOO=bar git status", true, "", "git status"},
		{"env command wraps inner", "env FOO=bar git status", true, "", "git status"},
		{"command prefix wraps inner", "command git status", true, "", "git status"},
		{"shell -c rewrites inner command", "bash -lc 'bun lint'", true, "", "bun lint"},
		{"dynamic shell command string passthrough", `bash -lc "$cmd"`, false, ReasonShellCommandString, ""},
		{"dynamic command token passthrough", `$cmd`, false, ReasonDynamicCommand, ""},
		{"assignment only passthrough", "FOO=bar", false, ReasonEnvAssignment, ""},
		{"env flags passthrough", "env -i X=1 git status", false, ReasonEnvFlags, ""},
		{"command -v passthrough", "command -v git", false, ReasonCommandLookup, ""},
		{"subshell passthrough", "(git status)", false, ReasonSubshell, ""},
		{"brace group passthrough", "{ git status; }", false, ReasonBraceGroup, ""},
		{"exec builtin passthrough", "exec node app.js", false, ReasonShellBuiltin, ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			le := Explain(tt.line)
			if len(le.Segments) != 1 {
				t.Fatalf("expected 1 segment, got %d", len(le.Segments))
			}
			s := le.Segments[0]
			if s.Wrapped != tt.wantWrapped {
				t.Fatalf("Wrapped = %v, want %v (reason %q)", s.Wrapped, tt.wantWrapped, s.Reason)
			}
			if tt.wantWrapped {
				if s.Inner != tt.wantInner {
					t.Errorf("Inner = %q, want %q", s.Inner, tt.wantInner)
				}
			} else if !contains(s.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want substring %q", s.Reason, tt.wantReason)
			}
		})
	}
}

func TestExplainCompoundSegments(t *testing.T) {
	le := Explain("cd /tmp && ls -la")
	if len(le.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(le.Segments))
	}
	if le.Segments[0].Wrapped || !contains(le.Segments[0].Reason, ReasonShellBuiltin) {
		t.Errorf("segment 0 = %+v, want builtin passthrough", le.Segments[0])
	}
	if !le.Segments[1].Wrapped {
		t.Errorf("segment 1 = %+v, want wrapped", le.Segments[1])
	}
}

func contains(s, sub string) bool {
	return sub != "" && len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
