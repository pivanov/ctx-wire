package tune

import (
	"os"
	"strings"
	"testing"

	"ctx-wire/internal/gain"
)

func TestBuildPreviewEmpty(t *testing.T) {
	p := BuildPreview(&gain.Summary{}, NewSanitizer("", ""), Options{})
	if p.HasData {
		t.Fatal("empty summary should have HasData=false")
	}
	if out := FormatPreview(p); !strings.Contains(out, "no commands recorded yet") {
		t.Fatalf("empty preview message:\n%s", out)
	}
}

func TestBuildPreviewSanitizesSamples(t *testing.T) {
	san := NewSanitizer("/Users/alice", "/Users/alice/work/repo")
	s := &gain.Summary{
		Commands: 2,
		Opportunities: []gain.OpportunityStat{
			{Program: "cat", Mode: "passthrough", Filter: "-",
				Sample: "cat /Users/alice/work/repo/src/app.ts", Count: 1, RawBytes: 40000, EmittedBytes: 40000},
			{Program: "deploy", Mode: "passthrough", Filter: "-",
				Sample: "deploy --token sk-ant-SECRETVALUE0123456789abcdef", Count: 1, RawBytes: 30000, EmittedBytes: 30000},
		},
	}
	out := FormatPreview(BuildPreview(s, san, Options{}))
	if !strings.Contains(out, "$PROJECT/src/app.ts") {
		t.Errorf("project path not redacted in preview:\n%s", out)
	}
	if strings.Contains(out, "SECRETVALUE") {
		t.Errorf("secret leaked into preview:\n%s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected redaction marker:\n%s", out)
	}
}

func TestPreviewStatesNoOutputSamplesAndPrivacy(t *testing.T) {
	s := &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{
		{Program: "cat", Mode: "passthrough", Filter: "-", Sample: "cat README.md", Count: 1, RawBytes: 9000, EmittedBytes: 9000},
	}}
	out := FormatPreview(BuildPreview(s, NewSanitizer("", ""), Options{}))
	for _, want := range []string{
		"Bundle manifest",
		"does not capture raw command output",
		"no network calls are made",
		"no process environment variables are included",
		"no full raw logs are included",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q:\n%s", want, out)
		}
	}
}

func TestPreviewTopCapsSamples(t *testing.T) {
	mk := func(prog string, emit int64) gain.OpportunityStat {
		return gain.OpportunityStat{Program: prog, Mode: "passthrough", Filter: "-", Sample: prog, Count: 1, RawBytes: emit, EmittedBytes: emit}
	}
	s := &gain.Summary{Commands: 3, Opportunities: []gain.OpportunityStat{mk("aaa", 30000), mk("bbb", 20000), mk("ccc", 10000)}}
	p := BuildPreview(s, NewSanitizer("", ""), Options{TopN: 2})
	if len(p.Samples) != 2 || p.Omitted != 1 {
		t.Fatalf("top cap: samples=%d omitted=%d", len(p.Samples), p.Omitted)
	}
	if !strings.Contains(FormatPreview(p), "1 more") {
		t.Fatalf("expected truncation note")
	}
}

func TestPreviewWritesNothing(t *testing.T) {
	// Preview is a pure transform: building and formatting it must touch no files.
	dir := t.TempDir()
	before, _ := os.ReadDir(dir)
	s := &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{
		{Program: "cat", Mode: "passthrough", Filter: "-", Sample: "cat /Users/alice/x", Count: 1, RawBytes: 9000, EmittedBytes: 9000},
	}}
	_ = FormatPreview(BuildPreview(s, NewSanitizer("/Users/alice", "/Users/alice/work"), Options{}))
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("preview must not create files: before=%d after=%d", len(before), len(after))
	}
}
