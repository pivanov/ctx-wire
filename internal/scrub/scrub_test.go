package scrub

import (
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	tests := []struct {
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
	}

	for _, tt := range tests {
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
