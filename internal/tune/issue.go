package tune

import (
	"fmt"
	"net/url"
	"strings"

	"ctx-wire/internal/gain"
	"ctx-wire/internal/ui"
)

// maxIssueURLLen is a conservative ceiling on the GitHub "new issue" URL. Many
// browsers and servers reject very long URLs, so beyond this we refuse to
// prefill via URL and fall back to printing the body.
const maxIssueURLLen = 8000

// Issue is a ready-to-file GitHub issue: a short title suggestion and a Markdown
// body. Both are built from the sanitized tune data, so they are safe to display
// or to place in a browser URL.
type Issue struct {
	Title string
	Body  string
}

// BuildIssue assembles a GitHub issue from a gain summary. It is a pure
// transform: it writes no files, makes no network calls, and captures no raw
// output. Command samples are classified on the original (matching ctx-wire
// explain/tune) but sanitized for display. bundlePath, when non-empty, is only
// mentioned in the attachment instructions; no bundle is created here.
func BuildIssue(s *gain.Summary, san Sanitizer, opts Options, meta BundleMeta, bundlePath string) Issue {
	if s.Commands == 0 {
		return Issue{
			Title: "ctx-wire tune: no commands recorded",
			Body:  emptyIssueBody(meta),
		}
	}
	rep := Analyze(nil, s)
	sanitizeReport(&rep, san)
	return Issue{
		Title: issueTitle(rep, s),
		Body:  issueBody(rep, s, meta, opts, bundlePath),
	}
}

// issueTitle produces a short, deterministic title that leads with the most
// actionable class.
func issueTitle(rep Report, s *gain.Summary) string {
	missing := len(rep.Sections[SectionMissingFilter])
	weak := len(rep.Sections[SectionWeakFilter])
	switch {
	case missing > 0:
		prog := rep.Sections[SectionMissingFilter][0].Program
		if missing == 1 {
			return fmt.Sprintf("ctx-wire tune: missing filter for %s", prog)
		}
		return fmt.Sprintf("ctx-wire tune: missing filter for %s (+%d more gaps)", prog, missing-1)
	case weak > 0:
		return fmt.Sprintf("ctx-wire tune: %d weak filter(s) to tune", weak)
	default:
		return fmt.Sprintf("ctx-wire tune: filter report (%d commands)", s.Commands)
	}
}

func emptyIssueBody(meta BundleMeta) string {
	var b strings.Builder
	b.WriteString("## ctx-wire tune report\n\n")
	b.WriteString("No commands were recorded in this window, so there is nothing to report yet.\n\n")
	writeEnvLine(&b, meta)
	b.WriteString("\n")
	writePrivacyChecklist(&b)
	return b.String()
}

func issueBody(rep Report, s *gain.Summary, meta BundleMeta, opts Options, bundlePath string) string {
	var b strings.Builder
	b.WriteString("## ctx-wire tune report\n\n")
	writeEnvLine(&b, meta)

	b.WriteString("\n### Summary\n\n")
	fmt.Fprintf(&b, "- Commands: %d\n", s.Commands)
	fmt.Fprintf(&b, "- Raw bytes: %s\n", ui.HumanBytes(s.RawBytes))
	fmt.Fprintf(&b, "- Emitted bytes: %s\n", ui.HumanBytes(s.EmittedBytes))
	fmt.Fprintf(&b, "- Saved: %s (%.1f%%)\n", ui.HumanBytes(s.SavedBytes), s.SavingsPct())
	fmt.Fprintf(&b, "- Window: %s\n", meta.Window)

	if classes := topClasses(s); len(classes) > 0 {
		b.WriteString("\n### Top classes\n\n")
		b.WriteString("| class | groups | executions | emitted |\n")
		b.WriteString("| --- | ---: | ---: | ---: |\n")
		for _, c := range classes {
			fmt.Fprintf(&b, "| %s | %d | %d | %s |\n", c.Class, c.Groups, c.Executions, ui.HumanBytes(c.EmittedBytes))
		}
	}

	writeSuggestions(&b, rep, opts)
	writeShapeHints(&b, rep, opts)

	b.WriteString("\n### Privacy checklist\n\n")
	writePrivacyChecklist(&b)

	b.WriteString("\n### Attaching a bundle\n\n")
	if bundlePath != "" {
		fmt.Fprintf(&b, "A sanitized bundle is available locally at `%s`. Attach it to this issue (drag and drop the file).\n", bundlePath)
	} else {
		b.WriteString("Generate a sanitized bundle and attach it to this issue (drag and drop the file):\n\n")
		b.WriteString("```sh\nctx-wire tune bundle --out ctx-wire-tune.tar.gz\n```\n")
	}
	b.WriteString("\nThe bundle never contains raw command output or environment variables; inspect it before sharing.\n")
	return b.String()
}

func writeEnvLine(b *strings.Builder, meta BundleMeta) {
	parts := []string{}
	if meta.Version != "" {
		v := meta.Version
		if meta.Commit != "" {
			v += " (" + meta.Commit + ")"
		}
		parts = append(parts, "ctx-wire "+v)
	}
	if meta.OS != "" && meta.Arch != "" {
		parts = append(parts, meta.OS+"/"+meta.Arch)
	}
	if meta.FilterCount > 0 {
		if meta.ConformanceCount > 0 {
			parts = append(parts, fmt.Sprintf("%d filters, %d conformance tests", meta.FilterCount, meta.ConformanceCount))
		} else {
			parts = append(parts, fmt.Sprintf("%d filters", meta.FilterCount))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "_%s_\n", strings.Join(parts, " | "))
	}
}

func writeSuggestions(b *strings.Builder, rep Report, opts Options) {
	any := false
	for _, sec := range sectionOrder {
		if len(rep.Sections[sec]) > 0 {
			any = true
			break
		}
	}
	if !any {
		b.WriteString("\n### Suggestions\n\n")
		b.WriteString("No filter gaps found: recorded commands are filtered well.\n")
		return
	}
	b.WriteString("\n### Suggestions\n")
	for _, sec := range sectionOrder {
		rows := rep.Sections[sec]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n**%s**\n\n", sectionTitle[sec])
		shown := rows
		if opts.TopN > 0 && len(rows) > opts.TopN {
			shown = rows[:opts.TopN]
		}
		for _, row := range shown {
			fmt.Fprintf(b, "- `%s` (emitted %s, saved %.1f%%, count %d): %s\n",
				row.Program, ui.HumanBytes(row.EmittedBytes), row.SavedPct, row.Count, row.Suggestion)
			writeMarkdownSample(b, row.Sample)
		}
		if opts.TopN > 0 && len(rows) > opts.TopN {
			fmt.Fprintf(b, "- _... %d more (raise --top to show)_\n", len(rows)-opts.TopN)
		}
	}
}

func writeShapeHints(b *strings.Builder, rep Report, opts Options) {
	if len(rep.ShapeHints) == 0 {
		return
	}
	b.WriteString("\n### Command-shape hints\n\n")
	shown := rep.ShapeHints
	if opts.TopN > 0 && len(shown) > opts.TopN {
		shown = shown[:opts.TopN]
	}
	for _, h := range shown {
		fmt.Fprintf(b, "- `%s`: %s\n", h.Program, h.Hint)
		writeMarkdownSample(b, h.Sample)
	}
	if opts.TopN > 0 && len(rep.ShapeHints) > opts.TopN {
		fmt.Fprintf(b, "- _... %d more (raise --top to show)_\n", len(rep.ShapeHints)-opts.TopN)
	}
}

func writeMarkdownSample(b *strings.Builder, sample string) {
	b.WriteString("  - sample:\n\n")
	for _, line := range strings.Split(sample, "\n") {
		fmt.Fprintf(b, "        %s\n", line)
	}
}

func writePrivacyChecklist(b *strings.Builder) {
	for _, line := range []string{
		"Secrets are scrubbed (scrub.Scrub), including split secret flags",
		"The user home is replaced with $HOME (matched at path boundaries)",
		"The project root is replaced with $PROJECT (matched at path boundaries)",
		"Long absolute paths are compacted, keeping the trailing segments",
		"Sample command length is capped",
		"No raw command output is included",
		"No process environment variables are included",
		"No full raw logs are included",
		"No network calls were made to produce this report",
	} {
		fmt.Fprintf(b, "- [x] %s\n", line)
	}
}

// IssueURL builds the GitHub "new issue" URL for repo (owner/name) with the
// title and body prefilled. The boolean is false when the resulting URL exceeds
// maxIssueURLLen, so the caller can fall back to printing the body instead of
// opening an over-long URL.
func IssueURL(repo string, issue Issue) (string, bool) {
	if !validGitHubRepoSlug(repo) {
		return "", false
	}
	q := url.Values{}
	q.Set("title", issue.Title)
	q.Set("body", issue.Body)
	u := "https://github.com/" + repo + "/issues/new?" + q.Encode()
	if len(u) > maxIssueURLLen {
		return "", false
	}
	return u, true
}

func validGitHubRepoSlug(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return false
	}
	return validGitHubPathPart(parts[0]) && validGitHubPathPart(parts[1])
}

func validGitHubPathPart(s string) bool {
	if s == "" || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// ParseGitHubRepo extracts "owner/name" from a git remote URL for github.com.
// It handles scp-style (git@github.com:owner/name.git), https, and ssh forms,
// and returns ok=false for non-GitHub or unparseable remotes.
func ParseGitHubRepo(remote string) (string, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", false
	}
	var path string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "ssh://git@github.com/"):
		path = strings.TrimPrefix(remote, "ssh://git@github.com/")
	case strings.HasPrefix(remote, "https://github.com/"):
		path = strings.TrimPrefix(remote, "https://github.com/")
	case strings.HasPrefix(remote, "http://github.com/"):
		path = strings.TrimPrefix(remote, "http://github.com/")
	default:
		return "", false
	}
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	repo := parts[0] + "/" + parts[1]
	if !validGitHubRepoSlug(repo) {
		return "", false
	}
	return repo, true
}
