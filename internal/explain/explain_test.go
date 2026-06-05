package explain

import (
	"strings"
	"testing"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/gain"
)

func mustReg(t *testing.T) *filter.Registry {
	t.Helper()
	reg, err := filter.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	return reg
}

func first(t *testing.T, r Report) SegmentReport {
	t.Helper()
	if len(r.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d: %+v", len(r.Segments), r.Segments)
	}
	return r.Segments[0]
}

func TestCommandMatchedFilter(t *testing.T) {
	s := first(t, Command(mustReg(t), "git status"))
	if !s.Wrapped || s.RunnerMode != ModeFiltered || s.Filter != "git-status" {
		t.Fatalf("got %+v, want wrapped filtered git-status", s)
	}
}

func TestCommandNoMatchedFilter(t *testing.T) {
	s := first(t, Command(mustReg(t), "frobnicate --all"))
	if !s.Wrapped || s.RunnerMode != ModeLive || s.FilterReason == "" {
		t.Fatalf("got %+v, want wrapped live-passthrough", s)
	}
}

func TestCommandPipelineWrapsLastStage(t *testing.T) {
	// A pipeline now wraps its final stage (producers stay raw); the last stage
	// is filtered, so the agent-facing output is reduced without changing the
	// intermediate stream.
	s := first(t, Command(mustReg(t), "cat log | grep err"))
	if !s.Wrapped || s.RunnerMode != ModeFiltered || s.Filter != "grep" {
		t.Fatalf("got %+v, want wrapped last stage filtered by grep", s)
	}
	if !strings.Contains(s.Rewritten, "cat log | ctx-wire run grep err") {
		t.Fatalf("rewritten = %q, want producer raw and last stage wrapped", s.Rewritten)
	}
}

func TestCommandPipelineUnwrappableLastStagePassthrough(t *testing.T) {
	// When the last stage cannot be wrapped (here a redirect), the whole pipeline
	// passes through unchanged.
	s := first(t, Command(mustReg(t), "cat log | grep err > out.txt"))
	if s.Wrapped || !strings.Contains(s.HookReason, "pipeline") {
		t.Fatalf("got %+v, want pipeline passthrough", s)
	}
}

func TestCommandRedirectPassthrough(t *testing.T) {
	s := first(t, Command(mustReg(t), "echo x > out.txt"))
	if s.Wrapped || !strings.Contains(s.HookReason, "redirection") {
		t.Fatalf("got %+v, want redirect passthrough", s)
	}
}

func TestCommandShellBuiltinPassthrough(t *testing.T) {
	s := first(t, Command(mustReg(t), "cd /tmp"))
	if s.Wrapped || !strings.Contains(s.HookReason, "builtin") {
		t.Fatalf("got %+v, want builtin passthrough", s)
	}
}

func TestCommandAlreadyWrappedPassthrough(t *testing.T) {
	s := first(t, Command(mustReg(t), "ctx-wire run git status"))
	if s.Wrapped || !strings.Contains(s.HookReason, "ctx-wire") {
		t.Fatalf("got %+v, want already-ctx-wire passthrough", s)
	}
}

func TestCommandFullPathNormalization(t *testing.T) {
	s := first(t, Command(mustReg(t), "/usr/local/bin/git status"))
	if !s.Wrapped || s.RunnerMode != ModeFiltered || s.Filter != "git-status" || !s.Normalized {
		t.Fatalf("got %+v, want filtered git-status via normalization", s)
	}
}

func TestCommandInteractiveBypass(t *testing.T) {
	s := first(t, Command(mustReg(t), "vim main.go"))
	if !s.Wrapped || s.RunnerMode != ModeBypass || !strings.Contains(s.BypassReason, "interactive") {
		t.Fatalf("got %+v, want inherited bypass interactive", s)
	}
}

func TestCommandTimePrefixFiltersInner(t *testing.T) {
	s := first(t, Command(mustReg(t), "time go test ./..."))
	if !s.Wrapped || s.RunnerMode != ModeFiltered || s.Filter != "go" {
		t.Fatalf("got %+v, want wrapped filtered go", s)
	}
	if s.Rewritten != "time ctx-wire run go test ./..." {
		t.Errorf("Rewritten = %q, want timed inner wrap", s.Rewritten)
	}
}

func TestCommandFollowBypass(t *testing.T) {
	s := first(t, Command(mustReg(t), "tail -f app.log"))
	if !s.Wrapped || s.RunnerMode != ModeBypass || !strings.Contains(s.BypassReason, "streaming") {
		t.Fatalf("got %+v, want inherited bypass streaming", s)
	}
}

func TestFormatOpportunitiesPassthroughAndWeakFiltered(t *testing.T) {
	s := &gain.Summary{
		Commands:     2,
		RawBytes:     13000,
		EmittedBytes: 12500,
		SavedBytes:   500,
		Opportunities: []gain.OpportunityStat{
			{
				Program: "cat", Mode: "passthrough", Filter: "-", Sample: "cat README.md",
				Count: 1, RawBytes: 10000, EmittedBytes: 10000, SavedBytes: 0,
			},
			{
				Program: "rg", Mode: "filtered", Filter: "rg", Sample: "rg func .*Explain|type internal",
				Count: 1, RawBytes: 3000, EmittedBytes: 2900, SavedBytes: 100,
			},
		},
	}
	out := FormatOpportunities(s)
	if !strings.Contains(out, "cat README.md") || !strings.Contains(out, "rg func .*Explain|type internal") {
		t.Errorf("missing sample commands:\n%s", out)
	}
	// cat is a common utility that ships a filter; passthrough groups it under
	// the common-utility-passthrough class with a broaden-filter hint.
	if !strings.Contains(out, "Common utility passthrough") {
		t.Errorf("missing common-utility passthrough class:\n%s", out)
	}
	if !strings.Contains(out, "broaden the existing filter") {
		t.Errorf("missing broaden-filter hint:\n%s", out)
	}
	// rg is a payload-style filtered row: conservative-filter class.
	if !strings.Contains(out, "Source/search payload (filtered conservatively)") {
		t.Errorf("missing payload class:\n%s", out)
	}
	if !strings.Contains(out, "tune only if repeated or unexpectedly large") {
		t.Errorf("missing payload hint:\n%s", out)
	}
	if !strings.Contains(out, "add -n/--line-number") {
		t.Errorf("missing rg line-number hint:\n%s", out)
	}
	// A regex pipe in a filtered sample must not be misread as a hook limitation.
	if strings.Contains(out, "Hook limitation") {
		t.Errorf("filtered command with regex pipe was misclassified as hook limitation:\n%s", out)
	}
}

func TestPayloadSearchLineNumberHintSuppressedWhenPresent(t *testing.T) {
	s := &gain.Summary{
		Commands: 1,
		Opportunities: []gain.OpportunityStat{
			{Program: "rg", Mode: "filtered", Filter: "rg", Sample: "rg -n customElement packages",
				Count: 1, RawBytes: 12000, EmittedBytes: 11500, SavedBytes: 0},
		},
	}
	out := FormatOpportunities(s)
	if strings.Contains(out, "add -n/--line-number") {
		t.Errorf("line-number hint should not appear when -n is already present:\n%s", out)
	}
}

func TestPayloadSearchLineNumberHintSuppressedForListingModes(t *testing.T) {
	for _, tt := range []struct {
		program string
		sample  string
	}{
		{"rg", "rg --files"},
		{"rg", "ctx-wire run rg --files"},
		{"rg", "rg -l customElement packages"},
		{"rg", "rg --files-with-matches customElement packages"},
		{"rg", "rg --files-without-match customElement packages"},
		{"rg", "rg --count customElement packages"},
		{"rg", "rg -c customElement packages"},
	} {
		if got := SearchLineNumberHint(tt.program, tt.sample); got != "" {
			t.Errorf("SearchLineNumberHint(%q, %q) = %q, want empty", tt.program, tt.sample, got)
		}
	}
}

func TestPayloadTeeLogHint(t *testing.T) {
	s := &gain.Summary{
		Commands: 1,
		Opportunities: []gain.OpportunityStat{
			{Program: "cat", Mode: "filtered", Filter: "cat", Sample: "cat /Users/me/.local/share/ctx-wire/tee/178_head.log",
				Count: 1, RawBytes: 600000, EmittedBytes: 590000, SavedBytes: 1000},
		},
	}
	out := FormatOpportunities(s)
	if !strings.Contains(out, "full ctx-wire spool log") || !strings.Contains(out, "head, tail, or sed -n") {
		t.Fatalf("tee-log hint missing:\n%s", out)
	}
}

func TestTeeLogHintPathForms(t *testing.T) {
	for _, sample := range []string{
		"cat ~/.local/share/ctx-wire/tee/178_head.log",
		"cat $HOME/.local/share/ctx-wire/tee/178_head.log",
		"ctx-wire run cat /Users/me/.local/share/ctx-wire/tee/178_head.log",
	} {
		if got := TeeLogHint(sample); got == "" {
			t.Fatalf("TeeLogHint(%q) = empty, want hint", sample)
		}
	}
	if got := TeeLogHint("cat /tmp/normal.log"); got != "" {
		t.Fatalf("TeeLogHint(normal log) = %q, want empty", got)
	}
}

func TestClassifyUnknownPassthroughAndToolingFiltered(t *testing.T) {
	s := &gain.Summary{
		Commands: 2,
		Opportunities: []gain.OpportunityStat{
			{Program: "frobnicate", Mode: "passthrough", Filter: "-", Sample: "frobnicate --all",
				Count: 1, RawBytes: 9000, EmittedBytes: 9000, SavedBytes: 0},
			{Program: "webpack", Mode: "filtered", Filter: "webpack", Sample: "webpack build",
				Count: 1, RawBytes: 9000, EmittedBytes: 8600, SavedBytes: 400},
		},
	}
	out := FormatOpportunities(s)
	if !strings.Contains(out, "Missing filter (unsupported command)") {
		t.Errorf("unknown passthrough should be in the missing-filter class:\n%s", out)
	}
	if !strings.Contains(out, "Filtered but weak") {
		t.Errorf("tooling filtered low-savings should be in the filtered-but-weak class:\n%s", out)
	}
}

func TestClassifyHookLimitationAndNotActionableFooter(t *testing.T) {
	s := &gain.Summary{
		Commands: 5, // 1 opportunity execution + 4 non-actionable rows
		Opportunities: []gain.OpportunityStat{
			{Program: "cat", Mode: "passthrough", Filter: "-", Sample: "cat app.log | grep err",
				Count: 1, RawBytes: 12000, EmittedBytes: 12000, SavedBytes: 0},
		},
	}
	out := FormatOpportunities(s)
	// A passthrough whose sample is a pipeline is a hook limitation, not a
	// missing/common-utility filter.
	if !strings.Contains(out, "Hook limitation") {
		t.Errorf("pipeline passthrough should be a hook limitation:\n%s", out)
	}
	if strings.Contains(out, "Common utility passthrough") || strings.Contains(out, "Missing filter") {
		t.Errorf("pipeline passthrough misclassified:\n%s", out)
	}
	// 5 recorded - 1 opportunity execution = 4 non-actionable, acknowledged not hidden.
	if !strings.Contains(out, "Not actionable") || !strings.Contains(out, "4 command(s)") {
		t.Errorf("missing not-actionable footer:\n%s", out)
	}
}

func TestClassifyGitPayloadFiltersAreNotWeak(t *testing.T) {
	cases := []struct {
		name string
		o    gain.OpportunityStat
	}{
		{"git diff", gain.OpportunityStat{Program: "git", Mode: ModeFiltered, Filter: "git-diff",
			Sample: "git diff", RawBytes: 20000, EmittedBytes: 19000, SavedBytes: 1000}},
		{"git status --short", gain.OpportunityStat{Program: "git", Mode: ModeFiltered, Filter: "git-status",
			Sample: "git status --short --untracked-files=all", RawBytes: 12000, EmittedBytes: 11500, SavedBytes: 500}},
		{"git branch", gain.OpportunityStat{Program: "git", Mode: ModeFiltered, Filter: "git-list",
			Sample: "git branch -a", RawBytes: 9000, EmittedBytes: 8800, SavedBytes: 200}},
		{"git log", gain.OpportunityStat{Program: "git", Mode: ModeFiltered, Filter: "git-log",
			Sample: "git log --oneline", RawBytes: 9000, EmittedBytes: 8800, SavedBytes: 200}},
		{"inline script", gain.OpportunityStat{Program: "bun", Mode: ModeFiltered, Filter: "inline-script",
			Sample: "bun -e 'console.log(1)'", RawBytes: 9000, EmittedBytes: 8800, SavedBytes: 200}},
		{"shell script", gain.OpportunityStat{Program: "bash", Mode: ModeFiltered, Filter: "shell-script",
			Sample: "bash /tmp/inv.sh", RawBytes: 9000, EmittedBytes: 8800, SavedBytes: 200}},
	}
	for _, c := range cases {
		if got := Classify(c.o); got != ClassPayloadConservative {
			t.Errorf("%s: Classify = %v, want ClassPayloadConservative (payload, not weak)", c.name, got)
		}
	}
}

func TestClassifyAwkLineReadIsPayload(t *testing.T) {
	// awk NR line-range / line-number reads are source reads, not weak filters.
	for _, sample := range []string{
		"awk 'NR>=507 && NR<=620' path/file.cs",
		"awk 'NR==10' file.go",
		"awk 'NR>=1 && NR<=50 {print}' main.rs",
		"awk 'NR>=1 && NR<=50 {print $0}' main.rs",
		`awk 'NR>=1 && NR<=180 {printf "%6d\t%s\n", NR, $0}' packages/theme/brand/exsto.css`,
		"ctx-wire run awk 'NR>=5 && NR<=9' a.txt",
		`awk '/FollowUpcustomer|VHCCustomer/{print NR": "$0}' path/file.cs`,
	} {
		o := gain.OpportunityStat{Program: "awk", Mode: ModeFiltered, Filter: "awk",
			Sample: sample, RawBytes: 12000, EmittedBytes: 11800, SavedBytes: 200}
		if got := Classify(o); got != ClassPayloadConservative {
			t.Errorf("Classify(%q) = %v, want ClassPayloadConservative", sample, got)
		}
	}
}

func TestClassifyAwkTransformStaysWeak(t *testing.T) {
	// Generic awk transforms/summarization with low savings stay weak: they
	// compute, aggregate, reformat, or join rather than simply project columns.
	for _, sample := range []string{
		"awk '{sum+=$3} END {print sum}' data.csv",
		"awk 'NR%2==0 {print $1}' file",
		"awk 'NR==FNR{seen[$1]=1; next} $1 in seen {print $0}' a b",
		"awk '/foo/{c++} END{print c}' log",
		"awk '{printf \"%s\\n\", $1}' file",
		"awk '{print $(NF-1)}' file",
		"awk '{print $1+$2}' nums",
	} {
		o := gain.OpportunityStat{Program: "awk", Mode: ModeFiltered, Filter: "awk",
			Sample: sample, RawBytes: 9000, EmittedBytes: 8600, SavedBytes: 400}
		if got := Classify(o); got != ClassFilteredWeak {
			t.Errorf("Classify(%q) = %v, want ClassFilteredWeak", sample, got)
		}
	}
}

func TestClassifyAwkFieldExtractIsPayload(t *testing.T) {
	// Simple awk column projection is payload (like cut): it prints positional
	// fields with no computation, aggregation, formatting, or control flow.
	for _, sample := range []string{
		"awk '{print $2}' file",
		"awk -F, '{print $1, $3}' data.csv",
		"awk '{print $1}' access.log",
		"awk '/error/{print $5}' app.log",
		"ctx-wire run awk '{print $NF}' file",
	} {
		o := gain.OpportunityStat{Program: "awk", Mode: ModeFiltered, Filter: "awk",
			Sample: sample, RawBytes: 12000, EmittedBytes: 11800, SavedBytes: 200}
		if got := Classify(o); got != ClassPayloadConservative {
			t.Errorf("Classify(%q) = %v, want ClassPayloadConservative", sample, got)
		}
	}
}

func TestClassifyUnixTransformProgramsArePayload(t *testing.T) {
	// The Unix transform family emits a re-shaped copy of its input (still the
	// content the agent wanted), so a low-savings filtered row is payload.
	cases := []struct{ program, sample string }{
		{"sort", "sort -u big.txt"},
		{"tr", "tr a-z A-Z"},
		{"cut", "cut -f2 -d, data.csv"},
		{"base64", "base64 payload.bin"},
		{"xargs", "xargs grep TODO"},
	}
	for _, c := range cases {
		o := gain.OpportunityStat{Program: c.program, Mode: ModeFiltered, Filter: c.program,
			Sample: c.sample, RawBytes: 12000, EmittedBytes: 11800, SavedBytes: 200}
		if got := Classify(o); got != ClassPayloadConservative {
			t.Errorf("Classify(%q) = %v, want ClassPayloadConservative", c.sample, got)
		}
	}
}

func TestClassifyToolingFilteredStaysWeak(t *testing.T) {
	// A non-payload tooling command with a real filter and low savings is still a
	// weak filter, not payload.
	o := gain.OpportunityStat{Program: "webpack", Mode: ModeFiltered, Filter: "webpack",
		Sample: "webpack build", RawBytes: 9000, EmittedBytes: 8600, SavedBytes: 400}
	if got := Classify(o); got != ClassFilteredWeak {
		t.Errorf("tooling filtered should stay ClassFilteredWeak, got %v", got)
	}
}

func TestFormatOpportunitiesGitPayloadGrouping(t *testing.T) {
	s := &gain.Summary{
		Commands: 1,
		Opportunities: []gain.OpportunityStat{
			{Program: "git", Mode: "filtered", Filter: "git-diff", Sample: "git diff",
				Count: 1, RawBytes: 20000, EmittedBytes: 19000, SavedBytes: 1000},
		},
	}
	out := FormatOpportunities(s)
	if !strings.Contains(out, "Source/search payload (filtered conservatively)") {
		t.Errorf("git diff should group under the payload class:\n%s", out)
	}
	if strings.Contains(out, "Filtered but weak") {
		t.Errorf("git diff must not be reported as a weak filter:\n%s", out)
	}
}

func TestFormatOpportunitiesEmptyAndNone(t *testing.T) {
	if out := FormatOpportunities(&gain.Summary{}); !strings.Contains(out, "no commands recorded yet") {
		t.Errorf("empty-log output = %q", out)
	}
	if out := FormatOpportunities(&gain.Summary{Commands: 5}); !strings.Contains(out, "no token opportunities found") {
		t.Errorf("no-opportunities output = %q", out)
	}
}
