package scrub

import (
	"strings"
	"testing"
)

var scrubCases = []struct {
	name      string
	in        string
	wantRedac bool     // expect [REDACTED] present
	mustKeep  []string // substrings that must survive
	mustDrop  []string // secret substrings that must be gone
}{
	{
		name:      "aws access key",
		in:        "key=AKIAIOSFODNN7EXAMPLE done",
		wantRedac: true,
		mustKeep:  []string{"done"},
		mustDrop:  []string{"AKIAIOSFODNN7EXAMPLE"},
	},
	{
		name:      "github token",
		in:        "token ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa here",
		wantRedac: true,
		mustDrop:  []string{"ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	},
	{
		name:      "jwt",
		in:        "auth eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.dozjgNryP4J3jVmNHl0w5N",
		wantRedac: true,
		mustDrop:  []string{"eyJhbGciOiJIUzI1NiJ9"},
	},
	{
		name:      "pem private key block",
		in:        "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIBVgIBADANBg\n-----END RSA PRIVATE KEY-----\nafter",
		wantRedac: true,
		mustKeep:  []string{"before", "after"},
		mustDrop:  []string{"MIIBVgIBADANBg", "BEGIN RSA PRIVATE KEY"},
	},
	{
		name:      "authorization bearer header keeps prefix",
		in:        "Authorization: Bearer abc123secrettoken",
		wantRedac: true,
		mustKeep:  []string{"Authorization: Bearer "},
		mustDrop:  []string{"abc123secrettoken"},
	},
	{
		name:      "secret assignment keeps key",
		in:        "PASSWORD=hunter2 OTHER=ok",
		wantRedac: true,
		mustKeep:  []string{"PASSWORD=", "OTHER=ok"},
		mustDrop:  []string{"hunter2"},
	},
	{
		name:      "single-quoted secret",
		in:        "PASSWORD='hunter2' next=ok",
		wantRedac: true,
		mustKeep:  []string{"PASSWORD=", "next=ok"},
		mustDrop:  []string{"hunter2"},
	},
	{
		// A double-quoted value containing an escaped quote must redact in
		// full; the old `"[^"]*"` value pattern stopped at the backslash-quote
		// and leaked the tail (PASSWORD=[REDACTED]bar" next=ok).
		name:      "double-quoted secret with escaped quote not partially leaked",
		in:        `PASSWORD="foo\"bar" next=ok`,
		wantRedac: true,
		mustKeep:  []string{"PASSWORD=", "next=ok"},
		mustDrop:  []string{"bar"},
	},
	{
		name:      "double-quoted secret with spaces",
		in:        `PASSWORD="hunter2 secret phrase" next=ok`,
		wantRedac: true,
		mustKeep:  []string{"PASSWORD=", "next=ok"},
		mustDrop:  []string{"hunter2", "secret phrase"},
	},
	{
		name:      "api key colon form",
		in:        "api_key: sk_test_abcdefghijklmnop1234",
		wantRedac: true,
		mustDrop:  []string{"sk_test_abcdefghijklmnop1234"},
	},
	{
		name:      "split secret long flag",
		in:        "deploy --password hunter2 --token 'a b c' --env prod",
		wantRedac: true,
		mustKeep:  []string{"--password ", "--token ", "--env prod"},
		mustDrop:  []string{"hunter2", "a b c"},
	},
	{
		name:      "url userinfo redacts only password",
		in:        "postgres://admin:s3cr3tP4ss@db.example.com:5432/app",
		wantRedac: true,
		mustKeep:  []string{"postgres://admin:", "@db.example.com:5432/app"},
		mustDrop:  []string{"s3cr3tP4ss"},
	},
	{
		name:      "vault service token with keyword",
		in:        "token = hvs.CAESIFakeVaultTokenValue000000000000",
		wantRedac: true,
		mustDrop:  []string{"hvs.CAESIFakeVaultTokenValue000000000000"},
	},
	{
		name:      "pypi token in isolation",
		in:        "pypi-AgEIcHlwaS5vcmcFakePypiTokenValue00000",
		wantRedac: true,
		mustDrop:  []string{"pypi-AgEIcHlwaS5vcmcFakePypiTokenValue00000"},
	},
	{
		name:      "vault token bare no keyword (prefilter regression guard)",
		in:        "hvs.CAESIFakeVaultTokenValue000000000000",
		wantRedac: true,
		mustDrop:  []string{"hvs.CAESIFakeVaultTokenValue000000000000"},
	},
	{
		name:      "aws control shape still redacts",
		in:        "key=AKIAIOSFODNN7EXAMPLE done",
		wantRedac: true,
		mustKeep:  []string{"done"},
		mustDrop:  []string{"AKIAIOSFODNN7EXAMPLE"},
	},
	{
		name:      "benign hover and pypiserver prose not redacted",
		in:        "Use hover.css from pypiserver to style buttons.",
		wantRedac: false,
		mustKeep:  []string{"hover.css", "pypiserver"},
	},
	{
		name:      "plain text untouched",
		in:        "Build succeeded. 0 warnings, 0 errors.",
		wantRedac: false,
		mustKeep:  []string{"Build succeeded. 0 warnings, 0 errors."},
	},
	{
		name:      "empty stays empty",
		in:        "",
		wantRedac: false,
	},
	{
		name:      "php strict-equality not redacted",
		in:        "if ($user->password === hash('x', $i)) {",
		wantRedac: false,
		mustKeep:  []string{"==="},
	},
	{
		name:      "loose-equality on token not redacted",
		in:        "token == $expected",
		wantRedac: false,
		mustKeep:  []string{"=="},
	},
	{
		name:      "php array arrow not redacted",
		in:        "secret => 'required'",
		wantRedac: false,
		mustKeep:  []string{"=>", "required"},
	},
	{
		name:      "real secret assignment still redacts",
		in:        "password = ghp_realsecret123 here",
		wantRedac: true,
		mustKeep:  []string{"here"},
		mustDrop:  []string{"ghp_realsecret123"},
	},
	{name: "db_pass assignment", in: "DB_PASS=fakeval123 ok", wantRedac: true, mustKeep: []string{"DB_PASS=", "ok"}, mustDrop: []string{"fakeval123"}},
	{name: "redis_pass assignment", in: "REDIS_PASS=fakeval123", wantRedac: true, mustDrop: []string{"fakeval123"}},
	{name: "pgpass assignment", in: "PGPASS=fakeval123", wantRedac: true, mustDrop: []string{"fakeval123"}},
	{name: "credential assignment", in: "credential=fakeopaque999", wantRedac: true, mustKeep: []string{"credential="}, mustDrop: []string{"fakeopaque999"}},
	{name: "credentials assignment", in: "credentials=fakeopaque999", wantRedac: true, mustDrop: []string{"fakeopaque999"}},
	{name: "redis url empty user", in: "redis://:fakepass@host:6379/0", wantRedac: true, mustKeep: []string{"redis://", "host:6379"}, mustDrop: []string{"fakepass"}},
}

func TestScrub(t *testing.T) {
	for _, tt := range scrubCases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := Scrub(tt.in)
			if tt.wantRedac && !strings.Contains(got, redacted) {
				t.Errorf("expected redaction in %q, got %q", tt.in, got)
			}
			if !tt.wantRedac && strings.Contains(got, redacted) {
				t.Errorf("unexpected redaction in %q, got %q", tt.in, got)
			}
			for _, keep := range tt.mustKeep {
				if !strings.Contains(got, keep) {
					t.Errorf("expected %q to survive, got %q", keep, got)
				}
			}
			for _, drop := range tt.mustDrop {
				if strings.Contains(got, drop) {
					t.Errorf("secret %q leaked through, got %q", drop, got)
				}
			}
		})
	}
}

func TestScrubArgs(t *testing.T) {
	args := []string{"deploy", "--token=ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--env=prod"}
	got := ScrubArgs(args)
	if got[0] != "deploy" || got[2] != "--env=prod" {
		t.Errorf("non-secret args mutated: %v", got)
	}
	if strings.Contains(got[1], "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Errorf("token leaked through ScrubArgs: %v", got)
	}
	if len(args) != 3 || strings.Contains(args[1], "REDACTED") {
		t.Errorf("ScrubArgs must not mutate the input slice: %v", args)
	}
}

// TestCommandLineMatchesCommand locks the invariant the discover matcher relies
// on: a raw shell command line, tokenized through CommandLine, yields the same
// canonical string that Command produces from the equivalent argv. So a quoted
// transcript command correlates with the de-quoted argv gain recorded.
func TestCommandLineMatchesCommand(t *testing.T) {
	tests := []struct {
		line string
		name string
		args []string
	}{
		{`sed -n '1,12p' file.txt`, "sed", []string{"-n", "1,12p", "file.txt"}},
		{`grep -n "allow" internal/permission/permission.go`, "grep", []string{"-n", "allow", "internal/permission/permission.go"}},
		{`go test ./...`, "go", []string{"test", "./..."}},
		{`echo "a b c"`, "echo", []string{"a b c"}},
		{`true --password hunter2`, "true", []string{"--password", "hunter2"}},
	}
	for _, tt := range tests {
		got := CommandLine(tt.line)
		want := Command(tt.name, tt.args)
		if got != want {
			t.Errorf("CommandLine(%q) = %q, want %q (== Command argv form)", tt.line, got, want)
		}
	}
}

func TestCommandRedactsSplitFlags(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		args     []string
		mustDrop []string
		mustKeep []string
	}{
		{
			name:     "split --password",
			cmd:      "true",
			args:     []string{"--password", "hunter2"},
			mustDrop: []string{"hunter2"},
			mustKeep: []string{"true", "--password", redacted},
		},
		{
			name:     "split --token and --api-key",
			cmd:      "deploy",
			args:     []string{"--token", "abc123", "--api-key", "xyz789", "--env", "prod"},
			mustDrop: []string{"abc123", "xyz789"},
			mustKeep: []string{"--token", "--api-key", "--env", "prod"},
		},
		{
			name:     "underscore and case normalized flag",
			cmd:      "x",
			args:     []string{"--API_KEY", "secretval123"},
			mustDrop: []string{"secretval123"},
			mustKeep: []string{"--API_KEY", redacted},
		},
		{
			name:     "inline form still redacted",
			cmd:      "x",
			args:     []string{"--password=hunter2"},
			mustDrop: []string{"hunter2"},
			mustKeep: []string{redacted},
		},
		{
			name:     "non-secret flag left alone",
			cmd:      "git",
			args:     []string{"commit", "-m", "fix"},
			mustKeep: []string{"git", "commit", "-m", "fix"},
		},
		{
			name:     "short flag not treated as secret",
			cmd:      "x",
			args:     []string{"-p", "notasecretport"},
			mustKeep: []string{"-p", "notasecretport"},
		},
		{
			name:     "shell metacharacters are quoted for display",
			cmd:      "rg",
			args:     []string{"-n", "func .*Explain|type", "internal"},
			mustKeep: []string{"rg", "-n", "'func .*Explain|type'", "internal"},
		},
		{
			name:     "single quotes are escaped inside display quotes",
			cmd:      "printf",
			args:     []string{"don't"},
			mustKeep: []string{"'don'\\''t'"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := Command(tt.cmd, tt.args)
			for _, d := range tt.mustDrop {
				if strings.Contains(got, d) {
					t.Errorf("secret %q leaked: %q", d, got)
				}
			}
			for _, k := range tt.mustKeep {
				if !strings.Contains(got, k) {
					t.Errorf("expected %q kept: %q", k, got)
				}
			}
		})
	}
}

func TestScrubFailClosed(t *testing.T) {
	out, ok := ScrubFailClosed("PASSWORD=hunter2")
	if !ok {
		t.Fatal("expected ok=true on normal input")
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("secret leaked: %q", out)
	}
}

func TestScrubFailClosedRecoversPanic(t *testing.T) {
	out, ok := scrubFailClosedWith("PASSWORD=hunter2", func(string) string {
		panic("boom")
	})
	if ok {
		t.Fatal("expected ok=false on scrub panic")
	}
	if out != "" {
		t.Fatalf("out = %q, want empty", out)
	}
}

// TestMightContainSecret pins mightContainSecret as a strict superset of the
// redaction rules. It asserts:
//  1. Every literalAnchor triggers the prefilter.
//  2. Every keywordRoot triggers the prefilter in both lower and upper case.
//  3. Clean strings return false.
//  4. Every TestScrub case with wantRedac:true also triggers the prefilter
//     (the load-bearing superset guard: if this breaks, the prefilter would
//     silently skip a string that Scrub would redact, leaking the secret).
func TestMightContainSecret(t *testing.T) {
	t.Run("literal anchors trigger", func(t *testing.T) {
		for _, anchor := range literalAnchors {
			s := "noise " + anchor + " noise"
			if !mightContainSecret(s) {
				t.Errorf("literalAnchor %q: mightContainSecret(%q) = false, want true", anchor, s)
			}
		}
	})

	t.Run("keyword roots trigger lower and upper", func(t *testing.T) {
		for _, root := range keywordRoots {
			lower := root + "=x"
			if !mightContainSecret(lower) {
				t.Errorf("keywordRoot %q (lower): mightContainSecret(%q) = false, want true", root, lower)
			}
			upper := strings.ToUpper(root) + "=x"
			if !mightContainSecret(upper) {
				t.Errorf("keywordRoot %q (upper): mightContainSecret(%q) = false, want true", root, upper)
			}
		}
	})

	t.Run("clean strings return false", func(t *testing.T) {
		clean := []string{
			"the quick brown fox jumps",
			"",
			"Build succeeded. 0 warnings, 0 errors.",
		}
		for _, s := range clean {
			if mightContainSecret(s) {
				t.Errorf("clean string: mightContainSecret(%q) = true, want false", s)
			}
		}
	})

	t.Run("superset guard: all TestScrub redacting inputs trigger prefilter", func(t *testing.T) {
		for _, c := range scrubCases {
			if !c.wantRedac {
				continue
			}
			if !mightContainSecret(c.in) {
				t.Errorf("superset guard FAILED for case %q: mightContainSecret(%q) = false, but Scrub would redact it (secret-leak gap in prefilter)", c.name, c.in)
			}
		}
	})
}

// TestTokenizeShell characterizes the current behavior of tokenizeShell for
// edge-case inputs. Expectations are derived from reading the function body
// directly (characterization tests of EXISTING behavior, not ideal behavior).
func TestTokenizeShell(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			// Empty input: loop never runs, inWord stays false, nil returned.
			name: "empty string",
			in:   "",
			want: nil,
		},
		{
			// Single bare word: default branch accumulates runes, final flush.
			name: "single bare word",
			in:   "git",
			want: []string{"git"},
		},
		{
			// Multiple spaces between words are all whitespace separators.
			name: "multiple spaces between words",
			in:   "git   status",
			want: []string{"git", "status"},
		},
		{
			// Single-quoted span: space inside quotes is not a word boundary.
			name: "single-quoted span with space",
			in:   "echo 'a b'",
			want: []string{"echo", "a b"},
		},
		{
			// Double-quoted span: same as single-quoted for plain content.
			name: "double-quoted span with space",
			in:   `echo "a b"`,
			want: []string{"echo", "a b"},
		},
		{
			// Unclosed single quote: all chars after the quote are consumed
			// into the current token (inWord stays true at EOF), so the
			// unterminated content is flushed as a normal token.
			name: "unclosed single quote",
			in:   "echo 'unterminated",
			want: []string{"echo", "unterminated"},
		},
		{
			// Trailing backslash as the very last rune: the escape condition
			// requires i+1 < len(runes), which is false, so the backslash
			// falls to the default branch and is written literally.
			name: "trailing backslash",
			in:   `echo foo\`,
			want: []string{"echo", `foo\`},
		},
		{
			// Embedded newline: \n matches the whitespace case and acts as a
			// word boundary, same as a space.
			name: "embedded newline",
			in:   "a\nb",
			want: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := tokenizeShell(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("tokenizeShell(%q) = %v (len %d), want %v (len %d)",
					tt.in, got, len(got), tt.want, len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("tokenizeShell(%q)[%d] = %q, want %q",
						tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}
