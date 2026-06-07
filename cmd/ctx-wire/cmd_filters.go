package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"ctx-wire/internal/draft"
	"ctx-wire/internal/filter"
	"ctx-wire/internal/ui"
)

// defaultRegistry is the community filter registry. A registry is a directory of
// standalone <name>.toml filter files (each with schema_version = 1 and inline
// [[tests]]); it can be an http(s) base URL or a local directory path.
const defaultRegistry = "https://raw.githubusercontent.com/pivanov/ctx-wire-filters/main"

const maxFilterBytes = 1 << 20

// fetchURL is a var so tests can stub network access. Redirects are disabled so
// the fetched content always comes from the origin the user named.
var fetchURL = func(url string) ([]byte, error) {
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxFilterBytes))
}

func cmdFilters(args []string) int {
	if isHelpArg(args) || len(args) == 0 {
		printHelp(os.Stdout, helpDoc{
			usage: []string{
				"ctx-wire filters pull <name> [--registry <url|dir>]",
				"ctx-wire filters publish <name>",
			},
			summary: "Share filters: pull a community filter (verified before install), or package a local one to publish.",
			notes: []string{
				"pull fetches a standalone filter, runs its inline tests locally, and installs it into .ctx-wire/filters.toml UNTRUSTED (run `ctx-wire trust` after reviewing). A filter is declarative TOML and Go's RE2 has no catastrophic backtracking, but a filter can still hide output, so review it (`ctx-wire inspect`) before trusting.",
				"publish prints a local filter as a standalone file ready to submit to the registry, with a reminder to genericize project-specific identifiers.",
			},
			examples: []string{
				"ctx-wire filters pull kubectl-get",
				"ctx-wire filters pull mytool --registry ./my-filters",
				"ctx-wire filters publish mytool",
			},
		})
		return 0
	}
	switch args[0] {
	case "pull":
		return cmdFiltersPull(args[1:])
	case "publish":
		return cmdFiltersPublish(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "ctx-wire filters: unknown subcommand %q (want pull or publish)\n", args[0])
		return 2
	}
}

var filterNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// validFilterName guards against path traversal / URL escape via the name.
func validFilterName(name string) bool {
	return filterNameRe.MatchString(name)
}

func fetchFilter(registry, name string) ([]byte, error) {
	registry = strings.TrimRight(registry, "/")
	if strings.HasPrefix(registry, "http://") || strings.HasPrefix(registry, "https://") {
		return fetchURL(registry + "/" + name + ".toml")
	}
	f, err := os.Open(filepath.Join(registry, name+".toml"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFilterBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFilterBytes {
		return nil, fmt.Errorf("filter file exceeds %d bytes", maxFilterBytes)
	}
	return data, nil
}

func cmdFiltersPull(args []string) int {
	registry := defaultRegistry
	name := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--registry":
			if i+1 >= len(args) {
				usageLine(os.Stderr, "ctx-wire filters pull <name> --registry <url|dir>")
				return 2
			}
			i++
			registry = args[i]
		case strings.HasPrefix(a, "--registry="):
			registry = strings.TrimPrefix(a, "--registry=")
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "ctx-wire filters pull: unknown flag %q\n", a)
			return 2
		default:
			if name != "" {
				fmt.Fprintln(os.Stderr, "ctx-wire filters pull: one filter at a time")
				return 2
			}
			name = a
		}
	}
	if name == "" {
		usageLine(os.Stderr, "ctx-wire filters pull <name> [--registry <url|dir>]")
		return 2
	}
	// Validate the name before it is interpolated into a path or URL, so a name
	// like "../../etc/passwd" cannot traverse a local registry or escape an http
	// base URL.
	if !validFilterName(name) {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: invalid filter name %q (use lowercase letters, digits, and -)\n", name)
		return 2
	}

	data, err := fetchFilter(registry, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %v\n", err)
		return 1
	}

	// Validate before installing: it must parse, carry schema_version = 1, every
	// inline test must pass, and it must not ship an unfinished draft marker.
	tmp, err := os.CreateTemp("", "ctxw-pull-*.toml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %v\n", err)
		return 1
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %v\n", err)
		return 1
	}
	if err := tmp.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %v\n", err)
		return 1
	}

	res, err := filter.VerifyFile(tmpName, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %q is not a valid filter file: %v\n", name, err)
		return 1
	}
	for _, o := range res.Outcomes {
		if o.Draft {
			fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %q ships an unfinished draft test; refusing\n", name)
			return 1
		}
		if !o.Passed {
			fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %q has a failing inline test (%s); refusing\n", name, o.TestName)
			return 1
		}
	}
	// The fetched file must define exactly the requested filter, so a filter
	// pulled as "mytool" cannot define [filters.git-status] and shadow a built-in
	// once the project file is trusted.
	if names := filterNames(string(data)); len(names) != 1 || names[0] != name {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %q must define exactly [filters.%s], got %v; refusing\n", name, name, names)
		return 1
	}

	theme := themeForStdout()
	if code := installPulledFilter(theme, string(data)); code != 0 {
		return code
	}
	fmt.Printf("%s review it before trusting: `ctx-wire inspect` shows what a filter strips on your own output\n", theme.Dim.Render("note:"))
	return 0
}

// installPulledFilter writes a fetched standalone filter into the project's
// .ctx-wire/filters.toml, untrusted, refusing on any [filters.<name>] collision
// and never producing a duplicate schema_version. Mirrors the draft writer.
func installPulledFilter(theme ui.Theme, pulled string) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %v\n", err)
		return 1
	}
	path := filter.ProjectFiltersPath(wd)
	names := filterNames(pulled)
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "ctx-wire filters pull: the fetched file defines no [filters.*]")
		return 1
	}

	var content string
	data, rerr := os.ReadFile(path)
	switch {
	case rerr == nil && len(data) > 0:
		if _, verr := filter.VerifyFile(path, ""); verr != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %s exists but is invalid (%v); fix it first\n", path, verr)
			return 1
		}
		for _, n := range names {
			if draft.HasFilter(string(data), n) {
				fmt.Fprintf(os.Stderr, "ctx-wire filters pull: [filters.%s] already exists in %s; remove it first\n", n, path)
				return 1
			}
		}
		content = strings.TrimRight(string(data), "\n") + "\n\n" + stripSchemaVersion(pulled)
	case rerr != nil && !os.IsNotExist(rerr):
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: cannot read %s: %v\n", path, rerr)
		return 1
	default:
		content = pulled // standalone, keeps its own schema_version
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %v\n", err)
		return 1
	}
	if err := atomicWrite(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters pull: %v\n", err)
		return 1
	}
	fmt.Printf("%s pulled [filters.%s] into %s (untrusted)\n", theme.Success(), strings.Join(names, ", "), theme.Path.Render(path))
	fmt.Printf("%s `ctx-wire trust` to load it for this project\n", theme.Dim.Render("then:"))
	return 0
}

func cmdFiltersPublish(args []string) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		usageLine(os.Stderr, "ctx-wire filters publish <name>")
		return 2
	}
	name := args[0]
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters publish: %v\n", err)
		return 1
	}
	path := filter.ProjectFiltersPath(wd)
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire filters publish: no project filters at %s\n", path)
		return 1
	}
	block, ok := extractFilterBlock(string(data), name)
	if !ok {
		fmt.Fprintf(os.Stderr, "ctx-wire filters publish: [filters.%s] not found in %s\n", name, path)
		return 1
	}

	theme := themeForStdout()
	fmt.Fprintf(os.Stderr, "%s review the inline test input below for project-specific paths, hostnames, or ids and genericize before submitting.\n\n", theme.Warn.Render("review:"))
	fmt.Print("schema_version = 1\n\n" + block)
	fmt.Fprintf(os.Stderr, "\n%s save the block above as %s.toml and open a PR to your registry\n", theme.Dim.Render("next:"), name)
	return 0
}

// filterNames returns the [filters.*] keys defined in a TOML document.
func filterNames(content string) []string {
	var f struct {
		Filters map[string]toml.Primitive `toml:"filters"`
	}
	if _, err := toml.Decode(content, &f); err != nil {
		return nil
	}
	names := make([]string, 0, len(f.Filters))
	for n := range f.Filters {
		names = append(names, n)
	}
	return names
}

// stripSchemaVersion removes a leading `schema_version = N` line so a fetched
// standalone filter can be appended to an existing file without a duplicate key.
func stripSchemaVersion(content string) string {
	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if isSchemaVersionAssignment(line) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimLeft(b.String(), "\n")
}

// isSchemaVersionAssignment matches a `schema_version = N` line (any spacing) but
// not a key that merely starts with the same text (e.g. schema_version_extra).
func isSchemaVersionAssignment(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "schema_version")
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(rest, " \t"), "=")
}

// extractFilterBlock returns the [filters.<name>] table plus its [[tests.<name>]]
// block from a project filter file, for publishing a single filter.
func extractFilterBlock(content, name string) (string, bool) {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	capturing := false
	found := false
	filterHdr := fmt.Sprintf("[filters.%s]", name)
	testHdr := fmt.Sprintf("[[tests.%s]]", name)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == filterHdr || t == testHdr:
			capturing = true
			found = true
			b.WriteString(l + "\n")
		case strings.HasPrefix(t, "[filters.") || strings.HasPrefix(t, "[[tests."):
			capturing = false
		case capturing:
			b.WriteString(l + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n", found
}
