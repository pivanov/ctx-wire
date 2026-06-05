package tune

import (
	"os"
	"strings"
	"testing"

	"ctx-wire/internal/gain"
)

func TestBuildIssueEmptyLog(t *testing.T) {
	issue := BuildIssue(&gain.Summary{}, NewSanitizer("", ""), Options{}, fixedMeta(), "")
	if !strings.Contains(issue.Title, "no commands recorded") {
		t.Fatalf("empty title = %q", issue.Title)
	}
	if !strings.Contains(issue.Body, "No commands were recorded") {
		t.Fatalf("empty body should explain itself:\n%s", issue.Body)
	}
	if !strings.Contains(issue.Body, "No network calls were made") {
		t.Fatalf("empty body should still carry the privacy checklist:\n%s", issue.Body)
	}
}

func TestBuildIssueNormal(t *testing.T) {
	issue := BuildIssue(sampleSummary(), NewSanitizer("", ""), Options{}, fixedMeta(), "")
	if !strings.Contains(issue.Title, "missing filter for frobnicate") {
		t.Errorf("title should lead with the top missing filter: %q", issue.Title)
	}
	for _, want := range []string{
		"## ctx-wire tune report",
		"ctx-wire 1.2.3 (abc1234)",
		"### Summary",
		"- Commands: 4",
		"### Top classes",
		"### Suggestions",
		"**Missing filters**",
		"frobnicate",
		"### Privacy checklist",
		"No network calls were made",
		"### Attaching a bundle",
	} {
		if !strings.Contains(issue.Body, want) {
			t.Errorf("issue body missing %q:\n%s", want, issue.Body)
		}
	}
}

func TestBuildIssueSanitizesSecretsAndPaths(t *testing.T) {
	san := NewSanitizer("/Users/alice", "/Users/alice/work")
	s := &gain.Summary{
		Commands: 1,
		Opportunities: []gain.OpportunityStat{
			{Program: "deploy", Mode: "passthrough", Filter: "-",
				Sample: "deploy /Users/alice/work/cfg --password hunter2",
				Count:  1, RawBytes: 40000, EmittedBytes: 40000},
		},
	}
	body := BuildIssue(s, san, Options{}, fixedMeta(), "").Body
	if strings.Contains(body, "hunter2") {
		t.Errorf("split secret flag leaked into issue body:\n%s", body)
	}
	if strings.Contains(body, "/Users/alice") {
		t.Errorf("raw home/project path leaked into issue body:\n%s", body)
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Errorf("expected redaction marker in issue body:\n%s", body)
	}
	if !strings.Contains(body, "$PROJECT/cfg") {
		t.Errorf("expected $PROJECT redaction in issue body:\n%s", body)
	}
}

func TestBuildIssueSamplesUseIndentedCodeBlocks(t *testing.T) {
	s := &gain.Summary{
		Commands: 1,
		Opportunities: []gain.OpportunityStat{
			{Program: "bun", Mode: "filtered", Filter: "inline-script",
				Sample: "bun -e 'console.log(`tick`)'\nsecond line",
				Count:  1, RawBytes: 40000, EmittedBytes: 40000},
		},
	}
	body := BuildIssue(s, NewSanitizer("", ""), Options{}, fixedMeta(), "").Body
	if strings.Contains(body, "sample: `bun -e") {
		t.Fatalf("sample should not be rendered as inline code:\n%s", body)
	}
	if !strings.Contains(body, "        bun -e 'console.log(`tick`)'") || !strings.Contains(body, "        second line") {
		t.Fatalf("multiline sample not rendered as an indented code block:\n%s", body)
	}
}

func TestBuildIssueTopCaps(t *testing.T) {
	mk := func(prog string, emit int64) gain.OpportunityStat {
		return gain.OpportunityStat{Program: prog, Mode: "passthrough", Filter: "-", Sample: prog, Count: 1, RawBytes: emit, EmittedBytes: emit}
	}
	s := &gain.Summary{Commands: 3, Opportunities: []gain.OpportunityStat{mk("aaa", 30000), mk("bbb", 20000), mk("ccc", 10000)}}
	body := BuildIssue(s, NewSanitizer("", ""), Options{TopN: 2}, fixedMeta(), "").Body
	if !strings.Contains(body, "aaa") || !strings.Contains(body, "bbb") {
		t.Errorf("top 2 should list the first two programs:\n%s", body)
	}
	if strings.Contains(body, "`ccc`") {
		t.Errorf("top 2 should hide the third program:\n%s", body)
	}
	if !strings.Contains(body, "1 more") {
		t.Errorf("top cap should note omissions:\n%s", body)
	}
}

func TestBuildIssueBundleMention(t *testing.T) {
	withPath := BuildIssue(sampleSummary(), NewSanitizer("", ""), Options{}, fixedMeta(), "/tmp/ctxw.tar.gz").Body
	if !strings.Contains(withPath, "/tmp/ctxw.tar.gz") {
		t.Errorf("issue should mention the provided bundle path:\n%s", withPath)
	}
	withoutPath := BuildIssue(sampleSummary(), NewSanitizer("", ""), Options{}, fixedMeta(), "").Body
	if !strings.Contains(withoutPath, "ctx-wire tune bundle --out") {
		t.Errorf("issue should instruct how to create a bundle:\n%s", withoutPath)
	}
}

func TestBuildIssueWritesNothing(t *testing.T) {
	dir := t.TempDir()
	before, _ := os.ReadDir(dir)
	_ = BuildIssue(sampleSummary(), NewSanitizer("/Users/alice", "/Users/alice/work"), Options{}, fixedMeta(), "/tmp/x.tar.gz")
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("BuildIssue must not create files: before=%d after=%d", len(before), len(after))
	}
}

func TestIssueURL(t *testing.T) {
	u, ok := IssueURL("owner/repo", Issue{Title: "my title", Body: "some body"})
	if !ok {
		t.Fatalf("IssueURL should succeed for a small issue")
	}
	if !strings.HasPrefix(u, "https://github.com/owner/repo/issues/new?") {
		t.Errorf("unexpected URL prefix: %s", u)
	}
	if !strings.Contains(u, "title=my+title") || !strings.Contains(u, "body=some+body") {
		t.Errorf("URL should carry url-encoded title/body: %s", u)
	}
}

func TestIssueURLTooLong(t *testing.T) {
	big := Issue{Title: "t", Body: strings.Repeat("x", maxIssueURLLen+1)}
	if _, ok := IssueURL("owner/repo", big); ok {
		t.Fatalf("IssueURL should refuse an over-long URL")
	}
}

func TestIssueURLRejectsInvalidRepoSlug(t *testing.T) {
	for _, repo := range []string{
		"",
		"owner",
		"owner/repo/extra",
		"owner/repo?tab=x",
		"owner/repo#frag",
		"owner/../repo",
		".owner/repo",
	} {
		if got, ok := IssueURL(repo, Issue{Title: "t", Body: "b"}); ok {
			t.Fatalf("IssueURL(%q) = %q,true; want ok=false", repo, got)
		}
	}
}

func TestParseGitHubRepo(t *testing.T) {
	good := map[string]string{
		"git@github.com:owner/repo.git":       "owner/repo",
		"git@github.com:owner/repo":           "owner/repo",
		"https://github.com/owner/repo.git":   "owner/repo",
		"https://github.com/owner/repo":       "owner/repo",
		"https://github.com/owner/repo/":      "owner/repo",
		"ssh://git@github.com/owner/repo.git": "owner/repo",
	}
	for in, want := range good {
		got, ok := ParseGitHubRepo(in)
		if !ok || got != want {
			t.Errorf("ParseGitHubRepo(%q) = %q,%v; want %q,true", in, got, ok, want)
		}
	}
	bad := []string{
		"",
		"git@gitlab.com:owner/repo.git",
		"https://example.com/owner/repo",
		"https://github.com/onlyowner",
		"https://github.com/owner/repo?tab=x",
		"https://github.com/owner/../repo",
		"not a url",
	}
	for _, in := range bad {
		if got, ok := ParseGitHubRepo(in); ok {
			t.Errorf("ParseGitHubRepo(%q) = %q,true; want ok=false", in, got)
		}
	}
}
