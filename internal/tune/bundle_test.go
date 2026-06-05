package tune

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"ctx-wire/internal/gain"
)

func fixedMeta() BundleMeta {
	return BundleMeta{
		GeneratedAt:      time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC),
		Version:          "1.2.3",
		Commit:           "abc1234",
		OS:               "linux",
		Arch:             "amd64",
		FilterCount:      130,
		ConformanceCount: 292,
		Window:           "all time",
	}
}

func sampleSummary() *gain.Summary {
	return &gain.Summary{
		Commands:     4,
		RawBytes:     102000,
		EmittedBytes: 100000,
		SavedBytes:   2000,
		Opportunities: []gain.OpportunityStat{
			{Program: "frobnicate", Mode: "passthrough", Filter: "-",
				Sample: "frobnicate --all big.txt", Count: 2, RawBytes: 40000, EmittedBytes: 40000},
			{Program: "webpack", Mode: "filtered", Filter: "webpack",
				Sample: "webpack build", Count: 1, RawBytes: 30000, EmittedBytes: 29000, SavedBytes: 1000},
			{Program: "git", Mode: "filtered", Filter: "git-diff",
				Sample: "git diff", Count: 1, RawBytes: 20000, EmittedBytes: 19000, SavedBytes: 1000},
		},
	}
}

func readArchive(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gr)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		out[hdr.Name] = b
	}
	return out
}

func TestBuildBundleContainsExpectedFiles(t *testing.T) {
	data, err := BuildBundle(sampleSummary(), NewSanitizer("", ""), Options{}, fixedMeta())
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	files := readArchive(t, data)
	for _, want := range []string{"summary.json", "report.txt", "suggestions.json", "samples/commands.jsonl", "privacy_report.txt"} {
		if _, ok := files[want]; !ok {
			t.Errorf("archive missing %s (have %v)", want, keys(files))
		}
	}
	if len(files) != 5 {
		t.Errorf("archive should have exactly 5 files, got %d: %v", len(files), keys(files))
	}
}

func TestBuildBundleSanitizesAndNoLeak(t *testing.T) {
	san := NewSanitizer("/Users/alice", "/Users/alice/work")
	s := &gain.Summary{
		Commands: 1,
		Opportunities: []gain.OpportunityStat{
			{Program: "deploy", Mode: "passthrough", Filter: "-",
				Sample: "deploy /Users/alice/work/cfg --token sk-ant-SECRETVALUE0123456789abcdef",
				Count:  1, RawBytes: 40000, EmittedBytes: 40000},
			{Program: "legacy", Mode: "passthrough", Filter: "-",
				Sample: "legacy --password hunter2",
				Count:  1, RawBytes: 12000, EmittedBytes: 12000},
		},
	}
	data, err := BuildBundle(s, san, Options{}, fixedMeta())
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	// Scan every byte of the decompressed archive: no raw secret, no raw home.
	var all bytes.Buffer
	for _, b := range readArchive(t, data) {
		all.Write(b)
	}
	blob := all.String()
	if strings.Contains(blob, "SECRETVALUE") {
		t.Errorf("secret leaked into bundle")
	}
	if strings.Contains(blob, "hunter2") {
		t.Errorf("split flag secret leaked into bundle")
	}
	if strings.Contains(blob, "/Users/alice") {
		t.Errorf("raw home/project path leaked into bundle")
	}
	if !strings.Contains(blob, "[REDACTED]") {
		t.Errorf("expected redaction marker in bundle")
	}
	if !strings.Contains(blob, "$PROJECT/cfg") {
		t.Errorf("expected $PROJECT redaction in bundle")
	}
}

func TestBuildBundleSummaryJSON(t *testing.T) {
	data, err := BuildBundle(sampleSummary(), NewSanitizer("", ""), Options{}, fixedMeta())
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	var sum bundleSummary
	if err := json.Unmarshal(readArchive(t, data)["summary.json"], &sum); err != nil {
		t.Fatalf("summary.json invalid: %v", err)
	}
	if sum.Commands != 4 || sum.SavedBytes != 2000 {
		t.Errorf("summary counts wrong: %+v", sum)
	}
	if sum.Version != "1.2.3" || sum.OS != "linux" || sum.FilterCount != 130 || sum.ConformanceTests != 292 {
		t.Errorf("summary meta wrong: %+v", sum)
	}
	if sum.Window != "all time" {
		t.Errorf("summary window wrong: %q", sum.Window)
	}
	if len(sum.TopClasses) == 0 || sum.TopClasses[0].EmittedBytes < sum.TopClasses[len(sum.TopClasses)-1].EmittedBytes {
		t.Errorf("top_classes should be present and sorted by emitted desc: %+v", sum.TopClasses)
	}
}

func TestBuildBundleSamplesJSONL(t *testing.T) {
	data, err := BuildBundle(sampleSummary(), NewSanitizer("", ""), Options{}, fixedMeta())
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(readArchive(t, data)["samples/commands.jsonl"])), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 sample lines, got %d", len(lines))
	}
	for _, line := range lines {
		var rec sampleRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("sample line not valid JSON: %v (%s)", err, line)
		}
		if rec.Program == "" || rec.Class == "" {
			t.Errorf("sample record missing program/class: %+v", rec)
		}
	}
}

func TestBuildBundlePrivacyReport(t *testing.T) {
	data, err := BuildBundle(sampleSummary(), NewSanitizer("", ""), Options{}, fixedMeta())
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	pr := string(readArchive(t, data)["privacy_report.txt"])
	for _, want := range []string{
		"No network calls were made.",
		"No command output is captured",
		"Process environment variables.",
		"Full raw gain logs.",
		"$HOME",
		"$PROJECT",
	} {
		if !strings.Contains(pr, want) {
			t.Errorf("privacy_report.txt missing %q:\n%s", want, pr)
		}
	}
}

func TestBuildBundleDeterministic(t *testing.T) {
	s, san, meta := sampleSummary(), NewSanitizer("/Users/alice", "/Users/alice/work"), fixedMeta()
	a, err := BuildBundle(s, san, Options{}, meta)
	if err != nil {
		t.Fatalf("BuildBundle a: %v", err)
	}
	b, err := BuildBundle(s, san, Options{}, meta)
	if err != nil {
		t.Fatalf("BuildBundle b: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("identical inputs should produce byte-identical archives (%d vs %d bytes)", len(a), len(b))
	}
}

func TestBuildBundleEmptySummary(t *testing.T) {
	data, err := BuildBundle(&gain.Summary{}, NewSanitizer("", ""), Options{}, fixedMeta())
	if err != nil {
		t.Fatalf("BuildBundle empty: %v", err)
	}
	files := readArchive(t, data)
	if len(files) != 5 {
		t.Fatalf("empty bundle should still have 5 files, got %d", len(files))
	}
	if !strings.Contains(string(files["report.txt"]), "no commands recorded yet") {
		t.Errorf("empty report.txt should explain itself:\n%s", files["report.txt"])
	}
}

func TestBuildBundleWritesNothing(t *testing.T) {
	// BuildBundle is a pure transform: it returns bytes and must touch no files.
	dir := t.TempDir()
	before, _ := os.ReadDir(dir)
	if _, err := BuildBundle(sampleSummary(), NewSanitizer("/Users/alice", "/Users/alice/work"), Options{}, fixedMeta()); err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("BuildBundle must not create files: before=%d after=%d", len(before), len(after))
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
