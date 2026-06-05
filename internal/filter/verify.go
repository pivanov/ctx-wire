package filter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// TestOutcome is the result of running one inline [[tests.*]] case.
type TestOutcome struct {
	FilterName string
	TestName   string
	Passed     bool
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
			actual := strings.TrimRight(Apply(cf, t.Input), "\n")
			expected := strings.TrimRight(t.Expected, "\n")
			res.Outcomes = append(res.Outcomes, TestOutcome{
				FilterName: name,
				TestName:   t.Name,
				Passed:     actual == expected,
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
