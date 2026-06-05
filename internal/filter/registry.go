package filter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"ctx-wire/filters"
	"ctx-wire/internal/paths"
)

// Registry holds compiled filters in priority order (first match wins).
type Registry struct {
	Filters []*CompiledFilter
}

// Find returns the most specific filter whose match_command matches the command
// line, or nil for passthrough. "Most specific" is the longest matched span, so
// a precise pattern (e.g. spring-boot's "gradle .*bootRun") wins over a broad
// one (e.g. "gradle"). Equal spans are broken by explicit priority, then by
// registry order, which preserves source precedence (project-local before
// user-global before built-in).
func (r *Registry) Find(command string) *CompiledFilter {
	if best := r.find(command); best != nil {
		return best
	}
	if normalized := normalizeCommandProgram(command); normalized != command {
		return r.find(normalized)
	}
	return nil
}

func (r *Registry) find(command string) *CompiledFilter {
	var best *CompiledFilter
	bestLen := -1
	for _, f := range r.Filters {
		loc := f.matchRegex.FindStringIndex(command)
		if loc == nil {
			continue
		}
		span := loc[1] - loc[0]
		if span > bestLen || (span == bestLen && best != nil && f.Priority > best.Priority) {
			best = f
			bestLen = span
		}
	}
	return best
}

func normalizeCommandProgram(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return command
	}
	i := strings.IndexAny(command, " \t")
	prog, rest := command, ""
	if i >= 0 {
		prog, rest = command[:i], command[i:]
	}
	if !strings.ContainsRune(prog, filepath.Separator) {
		return command
	}
	base := filepath.Base(prog)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return command
	}
	return base + rest
}

// parseAndCompile parses TOML content and compiles its filters. Bad individual
// filters are skipped with a warning to keep the registry resilient.
func parseAndCompile(content, source string) ([]*CompiledFilter, error) {
	var file tomlFile
	md, err := toml.Decode(content, &file)
	if err != nil {
		return nil, fmt.Errorf("TOML parse error in %s: %w", source, err)
	}
	if file.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported schema_version %d in %s (expected 1)", file.SchemaVersion, source)
	}
	// Strict field validation: an unrecognized key is almost always a typo (e.g.
	// strip_line_matching for strip_lines_matching) that would otherwise silently
	// no-op. Built-in filters are ours and tested, so an unknown field is a hard
	// error; project/user files are fail-open, so we warn and keep known fields.
	if und := md.Undecoded(); len(und) > 0 {
		keys := undecodedKeyStrings(und)
		if source == "builtin" {
			return nil, fmt.Errorf("unknown field(s) in %s filters: %s", source, strings.Join(keys, ", "))
		}
		warnf("%s filters: ignoring unrecognized field(s): %s", source, strings.Join(keys, ", "))
	}

	// Deterministic order: sort filter names.
	names := make([]string, 0, len(file.Filters))
	for name := range file.Filters {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]*CompiledFilter, 0, len(names))
	for _, name := range names {
		cf, err := compile(name, file.Filters[name])
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ctx-wire] warning: filter %q in %s: %v\n", name, source, err)
			continue
		}
		out = append(out, cf)
	}
	return out, nil
}

// undecodedKeyStrings renders the undecoded TOML keys as sorted dotted strings
// (e.g. "filters.demo.strip_line_matching") for a clear diagnostic.
func undecodedKeyStrings(keys []toml.Key) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.String())
	}
	sort.Strings(out)
	return out
}

// concatBuiltins reads every embedded *.toml, sorts by filename for determinism,
// and concatenates them under a single schema_version header.
func concatBuiltins() (string, error) {
	entries, err := filters.FS.ReadDir(".")
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("schema_version = 1\n\n")
	for _, name := range names {
		data, err := filters.FS.ReadFile(name)
		if err != nil {
			return "", err
		}
		b.Write(data)
		b.WriteString("\n\n")
	}
	return b.String(), nil
}

// compileBuiltin compiles the embedded built-in filters.
func compileBuiltin() ([]*CompiledFilter, error) {
	content, err := concatBuiltins()
	if err != nil {
		return nil, err
	}
	return parseAndCompile(content, "builtin")
}

// LoadBuiltin loads and compiles only the embedded built-in filters.
func LoadBuiltin() (*Registry, error) {
	compiled, err := compileBuiltin()
	if err != nil {
		return nil, err
	}
	return &Registry{Filters: compiled}, nil
}

// userFiltersPath returns ~/.config/ctx-wire/filters.toml (honoring
// XDG_CONFIG_HOME).
func userFiltersPath() (string, error) {
	base, err := paths.ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", "filters.toml"), nil
}

// ProjectFiltersPath returns projectDir/.ctx-wire/filters.toml.
func ProjectFiltersPath(projectDir string) string {
	return filepath.Join(projectDir, ".ctx-wire", "filters.toml")
}

// Load builds the layered registry in priority order: trusted project filters,
// then user-global filters, then built-in filters. Built-in must compile;
// project and user files are fail-open (a parse error is warned and skipped, so
// a broken filter file never breaks command execution). Ties in matching favor
// the earliest source (project > user > built-in), giving project overrides.
func Load(projectDir string) (*Registry, error) {
	var filters []*CompiledFilter

	if projectDir != "" {
		ppath := ProjectFiltersPath(projectDir)
		if fileExists(ppath) {
			switch {
			case !IsTrusted(ppath):
				warnf("project filters %s are not trusted; run `ctx-wire trust` to enable. Ignoring.", ppath)
			default:
				if f, err := loadFiltersFile(ppath, "project"); err != nil {
					warnf("project filters %s: %v (ignored)", ppath, err)
				} else {
					filters = append(filters, f...)
				}
			}
		}
	}

	if upath, err := userFiltersPath(); err == nil && fileExists(upath) {
		if f, err := loadFiltersFile(upath, "user"); err != nil {
			warnf("user filters %s: %v (ignored)", upath, err)
		} else {
			filters = append(filters, f...)
		}
	}

	builtin, err := compileBuiltin()
	if err != nil {
		return nil, err
	}
	filters = append(filters, builtin...)

	return &Registry{Filters: filters}, nil
}

func loadFiltersFile(path, source string) ([]*CompiledFilter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseAndCompile(string(data), source)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[ctx-wire] "+format+"\n", a...)
}
