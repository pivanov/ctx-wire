package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ctx-wire/internal/draft"
	"ctx-wire/internal/filter"
	"ctx-wire/internal/transcript"
	"ctx-wire/internal/ui"
)

// cmdTuneDraft scaffolds a starter filter for a program from a real captured
// Claude transcript sample. It prints to stdout by default; --write and
// --builtin are explicit, gated actions. It never trusts the file it writes.
func cmdTuneDraft(args []string) int {
	if isHelpArg(args) {
		printHelp(os.Stdout, helpDoc{
			usage: []string{
				"ctx-wire tune draft <program> [--preview]",
				"ctx-wire tune draft <program> --write [--builtin]",
			},
			summary: "Scaffold a starter filter for <program> from a real captured sample.",
			notes: []string{
				"Reads a scrubbed Claude transcript sample, infers transforms, and prints a draft filter with a real test case. Prints to stdout by default; no writes.",
				"--preview also shows the before/after and the savings. --write appends to .ctx-wire/filters.toml (it does NOT trust it). --builtin writes filters/<name>.toml after confirmation.",
				"The generated test is draft = true: trim its expected output and remove the marker, then `ctx-wire verify --project`.",
				"Only Claude transcripts carry paired output today; Codex is a fast-follow.",
			},
			examples: []string{
				"ctx-wire tune draft kubectl",
				"ctx-wire tune draft git --preview",
				"ctx-wire tune draft npm --write",
			},
		})
		return 0
	}

	var (
		program string
		preview bool
		write   bool
		builtin bool
		all     bool
		sampleN int
		cmdRe   *regexp.Regexp
		since   time.Time
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--preview":
			preview = true
		case a == "--write":
			write = true
		case a == "--builtin":
			builtin, write = true, true
		case a == "--all":
			all = true
		case a == "--sample":
			if i+1 >= len(args) {
				usageLine(os.Stderr, "ctx-wire tune draft <program> --sample N")
				return 2
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintln(os.Stderr, "ctx-wire tune draft: --sample must be a positive integer")
				return 2
			}
			sampleN = n - 1
		case a == "--command":
			if i+1 >= len(args) {
				usageLine(os.Stderr, "ctx-wire tune draft <program> --command <regex>")
				return 2
			}
			i++
			re, err := regexp.Compile(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "ctx-wire tune draft: bad --command regex: %v\n", err)
				return 2
			}
			cmdRe = re
		case a == "--since":
			if i+1 >= len(args) {
				usageLine(os.Stderr, "ctx-wire tune draft <program> --since <dur>")
				return 2
			}
			i++
			ts, err := parseSince(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "ctx-wire tune draft: %v\n", err)
				return 2
			}
			since = ts
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "ctx-wire tune draft: unknown flag %q\n", a)
			return 2
		default:
			if program != "" {
				fmt.Fprintln(os.Stderr, "ctx-wire tune draft: only one program at a time")
				return 2
			}
			program = a
		}
	}
	if program == "" {
		usageLine(os.Stderr, "ctx-wire tune draft <program> [--preview | --write [--builtin]]")
		return 2
	}

	opts := transcript.Options{Since: since, ClaudeDirs: claudeConfigDirs()}
	if !all {
		if wd, err := os.Getwd(); err == nil {
			opts.Project = wd
		}
	}
	execs, err := (transcript.Claude{}).Execs(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: %v\n", err)
		return 1
	}

	samples := draft.SelectSamples(execs, program)
	if cmdRe != nil {
		kept := samples[:0]
		for _, s := range samples {
			if cmdRe.MatchString(s.Command) {
				kept = append(kept, s)
			}
		}
		samples = kept
	}
	if len(samples) == 0 {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: no Claude sample with output yet for %q (Codex support is a fast-follow)\n", program)
		return 1
	}
	// Only draft from RAW samples: a command that already matches a filter had its
	// transcript output reduced, so seeding from it would produce a wrong draft.
	if reg, rerr := loadRegistry(); rerr == nil && reg != nil {
		raw := draft.RawSamples(samples, reg)
		if len(raw) == 0 {
			fmt.Fprintf(os.Stderr, "ctx-wire tune draft: every captured %q sample already matches a filter, so its output is already reduced; nothing raw to draft from\n", program)
			return 1
		}
		samples = raw
	}
	if sampleN >= len(samples) {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: --sample %d out of range (%d available)\n", sampleN+1, len(samples))
		return 1
	}
	sample := samples[sampleN]

	when := ""
	if !sample.When.IsZero() {
		when = sample.When.UTC().Format(time.RFC3339)
	}
	spec := draft.Infer(program, sample.Command, sample.Output, when)
	cf, err := filter.CompileDraft(spec.Name, spec.FilterSpec())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: compile: %v\n", err)
		return 1
	}
	expected := filter.Apply(cf, sample.Output)

	theme := themeForStdout()
	fmt.Fprintf(os.Stderr, "%s seeded from `%s` (%d sample(s) available; #%d)\n",
		theme.Dim.Render("draft:"), sample.Command, len(samples), sampleN+1)

	if !write {
		out, err := spec.TOML(sample.Output, expected, draft.Local)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire tune draft: render: %v\n", err)
			return 1
		}
		fmt.Print(out)
		if preview {
			printDraftPreview(theme, sample.Output, expected)
		}
		return 0
	}
	if builtin {
		return writeBuiltinDraft(theme, spec, sample.Output, expected)
	}
	return writeLocalDraft(theme, spec, sample.Output, expected)
}

func printDraftPreview(theme ui.Theme, sample, expected string) {
	raw, emitted, pct := draft.Savings(sample, expected)
	fmt.Fprintf(os.Stderr, "\n%s %d -> %d bytes (%.1f%% saved)\n",
		theme.Label.Render("savings:"), raw, emitted, pct)
	fmt.Fprintf(os.Stderr, "%s\n%s\n", theme.Dim.Render("--- before (sample) ---"), headLines(sample, 8))
	fmt.Fprintf(os.Stderr, "%s\n%s\n", theme.Dim.Render("--- after (filtered) ---"), headLines(expected, 8))
}

func writeLocalDraft(theme ui.Theme, spec draft.Spec, sample, expected string) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: %v\n", err)
		return 1
	}
	path := filter.ProjectFiltersPath(wd)

	// Decide the full new content BEFORE touching disk, so a malformed or
	// unreadable existing file is reported instead of being clobbered or
	// half-appended.
	var content string
	data, rerr := os.ReadFile(path)
	switch {
	case rerr == nil && len(data) > 0:
		// Validate the existing file first: it must parse and be schema_version 1,
		// or appending would produce a file that still does not load.
		if _, verr := filter.VerifyFile(path, ""); verr != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire tune draft: %s exists but is invalid (%v); fix it before drafting into it\n", path, verr)
			return 1
		}
		if draft.HasFilter(string(data), spec.Name) {
			fmt.Fprintf(os.Stderr, "ctx-wire tune draft: [filters.%s] already exists in %s; rename or edit it directly (refusing to append a duplicate table)\n", spec.Name, path)
			return 1
		}
		// Append the body WITHOUT a second schema_version header (a duplicate key
		// is a TOML parse error). The existing file already supplies it.
		body, err := spec.TOML(sample, expected, draft.Builtin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire tune draft: render: %v\n", err)
			return 1
		}
		content = strings.TrimRight(string(data), "\n") + "\n\n" + body
	case rerr != nil && !os.IsNotExist(rerr):
		// A real read error on an existing file: never clobber it.
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: cannot read %s: %v\n", path, rerr)
		return 1
	default: // missing or empty: a fresh standalone file
		out, err := spec.TOML(sample, expected, draft.Local)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire tune draft: render: %v\n", err)
			return 1
		}
		content = out
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: %v\n", err)
		return 1
	}
	if err := atomicWrite(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: %v\n", err)
		return 1
	}

	// Sanity-check the result: it must parse and the new test must pass.
	if res, err := filter.VerifyFile(path, spec.Name); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: wrote %s but it does not parse: %v\n", path, err)
		return 1
	} else if len(res.Outcomes) == 0 || !res.Outcomes[0].Passed {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: wrote %s but the generated test did not pass\n", path)
		return 1
	}

	fmt.Printf("%s wrote draft [filters.%s] to %s (untrusted)\n", theme.Success(), spec.Name, theme.Path.Render(path))
	fmt.Printf("%s draft test passes mechanically; edit expected and remove `draft = true`, then `ctx-wire verify --project`\n", theme.Dim.Render("next:"))
	fmt.Printf("%s `ctx-wire trust` to load it for this project\n", theme.Dim.Render("then:"))
	return 0
}

func writeBuiltinDraft(theme ui.Theme, spec draft.Spec, sample, expected string) int {
	out, err := spec.TOML(sample, expected, draft.Builtin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: render: %v\n", err)
		return 1
	}
	path := filepath.Join("filters", spec.Name+".toml")
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: %s already exists; rename or edit it directly\n", path)
		return 1
	}

	// Gated even inside the repo: the sample is real (scrubbed) output that may
	// still carry project-specific identifiers not fit for a shared filter.
	fmt.Fprintf(os.Stderr, "%s the embedded test input is a real, scrubbed sample. Review it for\n", theme.Warn.Render("review:"))
	fmt.Fprintln(os.Stderr, "        project-specific paths, hostnames, or ids and genericize before committing.")
	fmt.Fprint(os.Stderr, "\n"+out+"\n")
	if !confirmStdin(fmt.Sprintf("Write %s?", path)) {
		fmt.Fprintln(os.Stderr, "aborted; nothing written")
		return 1
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire tune draft: %v\n", err)
		return 1
	}
	fmt.Printf("%s wrote %s\n", theme.Success(), theme.Path.Render(path))
	fmt.Printf("%s trim expected, remove `draft = true`, and `ctx-wire verify %s` before committing\n", theme.Dim.Render("next:"), spec.Name)
	return 0
}

// atomicWrite writes content to a temp file in the target's directory and
// renames it into place, so a write never leaves a half-written or clobbered
// file behind.
func atomicWrite(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ctx-wire-draft-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func confirmStdin(prompt string) bool {
	fmt.Fprint(os.Stderr, prompt+" [y/N]: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func headLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, "\n")
}
