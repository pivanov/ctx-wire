package filter

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// TestOutcome is the result of running one inline [[tests.*]] case.
type TestOutcome struct {
	FilterName string
	TestName   string
	Passed     bool
	Draft      bool // the test carries draft = true (asserts nothing yet)
	Actual     string
	Expected   string
}

// VerifyResults aggregates inline-test outcomes plus filters that have no tests.
type VerifyResults struct {
	Outcomes           []TestOutcome
	FiltersWithoutTest []string
}

// VerifyBuiltin runs the inline tests embedded in the built-in filters. If
// filterName is non-empty, only that filter's tests run.
func VerifyBuiltin(filterName string) (*VerifyResults, error) {
	content, err := concatBuiltins()
	if err != nil {
		return nil, err
	}
	return runTests(content, filterName)
}

// VerifyFile runs the inline tests in a project or standalone filter file (e.g.
// .ctx-wire/filters.toml). It validates schema_version the way the registry does
// on load, so a file that would not load does not silently "verify clean". It is
// trust-free by design: it runs the file's own inline tests and never loads or
// applies the filters to real commands, so it must not require trust.
func VerifyFile(path, filterName string) (*VerifyResults, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var probe tomlFile
	if _, err := toml.Decode(string(data), &probe); err != nil {
		return nil, fmt.Errorf("TOML parse error in %s: %w", path, err)
	}
	if probe.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported schema_version %d in %s (expected 1)", probe.SchemaVersion, path)
	}
	return runTests(string(data), filterName)
}

func runTests(content, filterName string) (*VerifyResults, error) {
	var file tomlFile
	if _, err := toml.Decode(content, &file); err != nil {
		return nil, fmt.Errorf("TOML parse error: %w", err)
	}

	// Compile filters, tracking names.
	compiled := make(map[string]*CompiledFilter, len(file.Filters))
	allNames := make([]string, 0, len(file.Filters))
	for name, def := range file.Filters {
		allNames = append(allNames, name)
		cf, err := compile(name, def)
		if err != nil {
			return nil, fmt.Errorf("filter %q compile error: %w", name, err)
		}
		compiled[name] = cf
	}
	sort.Strings(allNames)

	// A specific filter was requested but does not exist: that is an error, not
	// a vacuous "0 passed, 0 failed" success.
	if filterName != "" {
		if _, ok := compiled[filterName]; !ok {
			return nil, fmt.Errorf("unknown filter %q", filterName)
		}
	}

	res := &VerifyResults{}
	tested := map[string]bool{}

	// Run tests in sorted filter-name order for stable output.
	testNames := make([]string, 0, len(file.Tests))
	for name := range file.Tests {
		testNames = append(testNames, name)
	}
	sort.Strings(testNames)

	for _, name := range testNames {
		if filterName != "" && name != filterName {
			continue
		}
		tested[name] = true
		cf, ok := compiled[name]
		if !ok {
			return nil, fmt.Errorf("[[tests.%s]] references unknown filter", name)
		}
		for _, t := range file.Tests[name] {
			out, rawLen := applyTestCase(cf, t)
			actual := strings.TrimRight(out, "\n")
			expected := strings.TrimRight(t.Expected, "\n")
			passed := actual == expected
			// A savings floor (used by split-stream tests) catches a filter that
			// runs on the wrong stream and reduces ~nothing. When Expected is set,
			// both must hold; with Expected empty, the author opted into a
			// savings-only assertion.
			if t.MinSavedPercent > 0 {
				saved := 0
				if rawLen > 0 {
					saved = 100 * (rawLen - len(actual)) / rawLen
				}
				meets := saved >= t.MinSavedPercent
				if t.Expected == "" {
					passed = meets
				} else {
					passed = passed && meets
				}
			}
			res.Outcomes = append(res.Outcomes, TestOutcome{
				FilterName: name,
				TestName:   t.Name,
				Passed:     passed,
				Draft:      t.Draft,
				Actual:     actual,
				Expected:   expected,
			})
		}
	}

	for _, name := range allNames {
		if filterName != "" && name != filterName {
			continue
		}
		if !tested[name] {
			res.FiltersWithoutTest = append(res.FiltersWithoutTest, name)
		}
	}

	return res, nil
}

// applyTestCase runs one inline test through the filter and returns the observed
// output plus the raw input length (for the savings assertion). A `failed = true`
// case is applied the way the runner applies output from a non-zero exit (suppress
// synthetic-success messages, keep the tail on truncation). When stdout/stderr are
// set, it emulates the runner's stream selection so a filter aimed at the wrong
// stream is caught: with filter_stderr the filter sees stdout+stderr; without it,
// the filter sees stdout only and the raw stderr follows (as the runner prints it).
func applyTestCase(cf *CompiledFilter, t tomlTest) (string, int) {
	opts := ApplyOptions{}
	if t.Failed {
		opts.SuppressSyntheticSuccess = true
		opts.KeepTailOnTruncate = true
	}
	if t.Stdout != "" || t.Stderr != "" {
		raw := t.Stdout + t.Stderr
		if cf.FilterStderr {
			return ApplyWithMetaOptions(cf, raw, opts).Output, len(raw)
		}
		return ApplyWithMetaOptions(cf, t.Stdout, opts).Output + t.Stderr, len(raw)
	}
	return ApplyWithMetaOptions(cf, t.Input, opts).Output, len(t.Input)
}

// builtinFilterNames returns the sorted list of embedded filter names. Used for
// the "list all filters" goal of ctx-wire verify.
func builtinFilterNames() ([]string, error) {
	content, err := concatBuiltins()
	if err != nil {
		return nil, err
	}
	var file tomlFile
	if _, err := toml.Decode(content, &file); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(file.Filters))
	for name := range file.Filters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
