package tune

import (
	"strings"
	"testing"

	"ctx-wire/internal/gain"
)

// fixtures returns representative gain opportunities, one per report class, so
// the analysis and formatting tests share a single realistic data set.
func missingFilterOpp() gain.OpportunityStat {
	return gain.OpportunityStat{
		Program: "frobnicate", Mode: "passthrough", Filter: "-", Sample: "frobnicate --all",
		Count: 2, RawBytes: 40000, EmittedBytes: 40000, SavedBytes: 0,
	}
}

func weakFilterOpp() gain.OpportunityStat {
	return gain.OpportunityStat{
		Program: "webpack", Mode: "filtered", Filter: "webpack", Sample: "webpack build",
		Count: 1, RawBytes: 30000, EmittedBytes: 29000, SavedBytes: 1000,
	}
}

func payloadOpp() gain.OpportunityStat {
	return gain.OpportunityStat{
		Program: "cat", Mode: "filtered", Filter: "cat", Sample: "cat big.txt",
		Count: 1, RawBytes: 15000, EmittedBytes: 14800, SavedBytes: 200,
	}
}

func searchOpp() gain.OpportunityStat {
	return gain.OpportunityStat{
		Program: "rg", Mode: "filtered", Filter: "rg", Sample: "rg fetchData packages",
		Count: 1, RawBytes: 20000, EmittedBytes: 19500, SavedBytes: 500,
	}
}

func TestAnalyzeEmpty(t *testing.T) {
	r := Analyze(nil, &gain.Summary{})
	if r.HasData {
		t.Fatalf("expected HasData false for empty summary")
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "no commands recorded yet") {
		t.Fatalf("empty report should explain itself:\n%s", out)
	}
}

func TestAnalyzeMissingFilter(t *testing.T) {
	r := Analyze(nil, &gain.Summary{Commands: 2, Opportunities: []gain.OpportunityStat{missingFilterOpp()}})
	rows := r.Sections[SectionMissingFilter]
	if len(rows) != 1 || rows[0].Program != "frobnicate" {
		t.Fatalf("expected one missing-filter row, got %+v", rows)
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "Missing filters") || !strings.Contains(out, "add a built-in filter") {
		t.Fatalf("missing-filter report wrong:\n%s", out)
	}
}

func TestAnalyzeWeakFilter(t *testing.T) {
	r := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{weakFilterOpp()}})
	if len(r.Sections[SectionWeakFilter]) != 1 {
		t.Fatalf("expected one weak-filter row, got %+v", r.Sections[SectionWeakFilter])
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "Weak filters") || !strings.Contains(out, "review or tune the filter") {
		t.Fatalf("weak-filter report wrong:\n%s", out)
	}
}

func TestAnalyzePayloadNotBadFilter(t *testing.T) {
	r := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{payloadOpp()}})
	if len(r.Sections[SectionPayload]) != 1 {
		t.Fatalf("expected one payload row, got %+v", r.Sections[SectionPayload])
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "Payload commands") || !strings.Contains(out, "expected payload") {
		t.Fatalf("payload report should describe expected payload:\n%s", out)
	}
	if strings.Contains(out, "bad filter") {
		t.Fatalf("payload command must not be called a bad filter:\n%s", out)
	}
}

func TestAnalyzeGitFiltersArePayloadNotWeak(t *testing.T) {
	gitDiff := gain.OpportunityStat{Program: "git", Mode: "filtered", Filter: "git-diff",
		Sample: "git diff", Count: 1, RawBytes: 20000, EmittedBytes: 19000, SavedBytes: 1000}
	gitStatus := gain.OpportunityStat{Program: "git", Mode: "filtered", Filter: "git-status",
		Sample: "git status --short --untracked-files=all", Count: 1, RawBytes: 12000, EmittedBytes: 11500, SavedBytes: 500}
	r := Analyze(nil, &gain.Summary{Commands: 2, Opportunities: []gain.OpportunityStat{gitDiff, gitStatus}})
	if len(r.Sections[SectionPayload]) != 2 {
		t.Fatalf("git diff/status should be payload rows, got payload=%+v", r.Sections[SectionPayload])
	}
	if len(r.Sections[SectionWeakFilter]) != 0 {
		t.Fatalf("git diff/status must not be weak filters: %+v", r.Sections[SectionWeakFilter])
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "Payload commands") || strings.Contains(out, "Weak filters") {
		t.Fatalf("git payload commands misreported:\n%s", out)
	}
}

func TestAnalyzeAwkRangeReadIsPayloadNotWeak(t *testing.T) {
	rangeRead := gain.OpportunityStat{Program: "awk", Mode: "filtered", Filter: "awk",
		Sample: "awk 'NR>=507 && NR<=620' path/file.cs", Count: 1, RawBytes: 12000, EmittedBytes: 11800, SavedBytes: 200}
	transform := gain.OpportunityStat{Program: "awk", Mode: "filtered", Filter: "awk",
		Sample: "awk '{sum+=$3} END {print sum}' data.csv", Count: 1, RawBytes: 9000, EmittedBytes: 8600, SavedBytes: 400}
	r := Analyze(nil, &gain.Summary{Commands: 2, Opportunities: []gain.OpportunityStat{rangeRead, transform}})

	payload := r.Sections[SectionPayload]
	weak := r.Sections[SectionWeakFilter]
	if len(payload) != 1 || payload[0].Program != "awk" {
		t.Fatalf("awk range read should be a payload row, got payload=%+v", payload)
	}
	if len(weak) != 1 || weak[0].Sample != transform.Sample {
		t.Fatalf("generic awk transform should stay weak, got weak=%+v", weak)
	}
}

func TestAnalyzeUnixTransformsArePayloadNotWeak(t *testing.T) {
	mk := func(program, sample string) gain.OpportunityStat {
		return gain.OpportunityStat{Program: program, Mode: "filtered", Filter: program,
			Sample: sample, Count: 1, RawBytes: 12000, EmittedBytes: 11800, SavedBytes: 200}
	}
	payloadOpps := []gain.OpportunityStat{
		mk("sort", "sort -u big.txt"),
		mk("tr", "tr a-z A-Z"),
		mk("cut", "cut -f2 -d, data.csv"),
		mk("base64", "base64 payload.bin"),
		mk("xargs", "xargs grep TODO"),
		{Program: "awk", Mode: "filtered", Filter: "awk", Sample: "awk '{print $2}' access.log",
			Count: 1, RawBytes: 12000, EmittedBytes: 11800, SavedBytes: 200},
	}
	aggregation := gain.OpportunityStat{Program: "awk", Mode: "filtered", Filter: "awk",
		Sample: "awk '{sum+=$3} END {print sum}' data.csv", Count: 1, RawBytes: 9000, EmittedBytes: 8600, SavedBytes: 400}

	r := Analyze(nil, &gain.Summary{Commands: len(payloadOpps) + 1,
		Opportunities: append(append([]gain.OpportunityStat{}, payloadOpps...), aggregation)})

	if len(r.Sections[SectionPayload]) != len(payloadOpps) {
		t.Fatalf("expected %d payload rows, got %+v", len(payloadOpps), r.Sections[SectionPayload])
	}
	weak := r.Sections[SectionWeakFilter]
	if len(weak) != 1 || weak[0].Sample != aggregation.Sample {
		t.Fatalf("awk aggregation should stay weak, got weak=%+v", weak)
	}
}

func TestShapeHintSearchWithoutLineNumber(t *testing.T) {
	r := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{searchOpp()}})
	if len(r.ShapeHints) != 1 || r.ShapeHints[0].Program != "rg" {
		t.Fatalf("expected one rg shape hint, got %+v", r.ShapeHints)
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "Command-shape hints") || !strings.Contains(out, "add -n/--line-number") {
		t.Fatalf("rg-without-n hint missing:\n%s", out)
	}
}

func TestShapeHintSearchSuppressedWhenLineNumberPresent(t *testing.T) {
	opp := searchOpp()
	opp.Sample = "rg -n fetchData packages"
	r := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{opp}})
	if len(r.ShapeHints) != 0 {
		t.Fatalf("line-number hint should be suppressed when -n present: %+v", r.ShapeHints)
	}
}

func TestShapeHintSearchSuppressedForFileListing(t *testing.T) {
	opp := searchOpp()
	opp.Sample = "rg --files"
	r := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{opp}})
	if len(r.ShapeHints) != 0 {
		t.Fatalf("line-number hint should be suppressed for rg --files: %+v", r.ShapeHints)
	}
}

func TestShapeHintBroadFind(t *testing.T) {
	broad := gain.OpportunityStat{Program: "find", Mode: "passthrough", Sample: "find . -size +1M",
		Count: 1, RawBytes: 12000, EmittedBytes: 12000}
	scoped := gain.OpportunityStat{Program: "find", Mode: "passthrough", Sample: "find . -name *.go",
		Count: 1, RawBytes: 12000, EmittedBytes: 12000}

	if got := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{broad}}); len(got.ShapeHints) != 1 {
		t.Fatalf("broad find should emit a shape hint: %+v", got.ShapeHints)
	}
	if got := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{scoped}}); len(got.ShapeHints) != 0 {
		t.Fatalf("scoped find should not emit a shape hint: %+v", got.ShapeHints)
	}
}

func TestShapeHintRepeatedAbsolutePaths(t *testing.T) {
	opp := gain.OpportunityStat{Program: "cat", Mode: "filtered", Filter: "cat",
		Sample: "cat /Users/me/proj/a.txt /Users/me/proj/b.txt",
		Count:  1, RawBytes: 15000, EmittedBytes: 14800, SavedBytes: 200}
	r := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{opp}})
	found := false
	for _, h := range r.ShapeHints {
		if strings.Contains(h.Hint, "repeats absolute paths") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected repeated-absolute-paths hint: %+v", r.ShapeHints)
	}
}

func TestShapeHintTeeLog(t *testing.T) {
	opp := gain.OpportunityStat{Program: "cat", Mode: "filtered", Filter: "cat",
		Sample: "cat /Users/me/.local/share/ctx-wire/tee/178_head.log",
		Count:  1, RawBytes: 600000, EmittedBytes: 590000, SavedBytes: 1000}
	r := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{opp}})
	if len(r.ShapeHints) != 1 || !strings.Contains(r.ShapeHints[0].Hint, "full ctx-wire spool log") {
		t.Fatalf("expected tee-log shape hint, got %+v", r.ShapeHints)
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "full ctx-wire spool log") {
		t.Fatalf("formatted tee-log hint missing:\n%s", out)
	}
}

func TestAnalyzeHookLimitationNotMissingFilter(t *testing.T) {
	opp := gain.OpportunityStat{Program: "cat", Mode: "passthrough", Filter: "-",
		Sample: "cat app.log | grep err", Count: 1, RawBytes: 12000, EmittedBytes: 12000}
	r := Analyze(nil, &gain.Summary{Commands: 1, Opportunities: []gain.OpportunityStat{opp}})
	if len(r.Sections[SectionMissingFilter]) != 0 {
		t.Fatalf("pipeline passthrough must not be a missing-filter row: %+v", r.Sections)
	}
	if r.HookLimited != 1 {
		t.Fatalf("pipeline passthrough should be acknowledged as a hook limitation, got %d", r.HookLimited)
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "Hook limitations") {
		t.Fatalf("hook-limitation footer missing:\n%s", out)
	}
}

func TestAnalyzeNotActionableFooter(t *testing.T) {
	// 5 recorded, 2 opportunity executions -> 3 non-actionable.
	r := Analyze(nil, &gain.Summary{Commands: 5, Opportunities: []gain.OpportunityStat{missingFilterOpp()}})
	// missingFilterOpp has Count 2, so 5 - 2 = 3 non-actionable.
	if r.LowVolume != 3 {
		t.Fatalf("not-actionable count = %d, want 3", r.LowVolume)
	}
	out := Format(r, Options{})
	if !strings.Contains(out, "Not actionable") || !strings.Contains(out, "3 command(s)") {
		t.Fatalf("not-actionable footer wrong:\n%s", out)
	}
}

func TestFormatTopCapsRows(t *testing.T) {
	a := gain.OpportunityStat{Program: "aaa", Mode: "passthrough", Filter: "-", Sample: "aaa", Count: 1, RawBytes: 30000, EmittedBytes: 30000}
	b := gain.OpportunityStat{Program: "bbb", Mode: "passthrough", Filter: "-", Sample: "bbb", Count: 1, RawBytes: 20000, EmittedBytes: 20000}
	c := gain.OpportunityStat{Program: "ccc", Mode: "passthrough", Filter: "-", Sample: "ccc", Count: 1, RawBytes: 10000, EmittedBytes: 10000}
	r := Analyze(nil, &gain.Summary{Commands: 3, Opportunities: []gain.OpportunityStat{a, b, c}})

	out := Format(r, Options{TopN: 2})
	if !strings.Contains(out, "aaa") || !strings.Contains(out, "bbb") {
		t.Fatalf("top 2 should show the first two rows:\n%s", out)
	}
	if strings.Contains(out, "  - ccc") {
		t.Fatalf("top 2 should hide the third row:\n%s", out)
	}
	if !strings.Contains(out, "1 more") {
		t.Fatalf("truncation note missing:\n%s", out)
	}

	full := Format(r, Options{})
	if !strings.Contains(full, "  - ccc") {
		t.Fatalf("no --top should show every row:\n%s", full)
	}
}

func TestAnalyzeNoGapsMessage(t *testing.T) {
	// A summary with commands but no opportunities reports a clean bill.
	out := Format(Analyze(nil, &gain.Summary{Commands: 5}), Options{})
	if !strings.Contains(out, "no filter gaps found") {
		t.Fatalf("expected clean-bill message:\n%s", out)
	}
}

func TestFormatPlainHasNoANSI(t *testing.T) {
	r := Analyze(nil, &gain.Summary{Commands: 4, Opportunities: []gain.OpportunityStat{
		missingFilterOpp(), weakFilterOpp(), payloadOpp(), searchOpp(),
	}})
	out := Format(r, Options{})
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("plain Format should not contain ANSI escapes:\n%q", out)
	}
}
