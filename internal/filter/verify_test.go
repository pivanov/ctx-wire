package filter

import (
	"fmt"
	"testing"
)

// TestSplitStreamFixtureGuard proves the class-closing fixture: a [[tests]] case
// with diagnostics on `stderr` and a `min_saved_percent` floor passes only when
// the filter actually processes stderr (filter_stderr = true). Without it the
// filter sees an empty stdout, the raw stderr passes through, savings -> ~0, and
// the test fails. This is the regression guard a single-stream exact-match cannot
// be (it caught nothing when biome shipped without filter_stderr).
func TestSplitStreamFixtureGuard(t *testing.T) {
	tmpl := `schema_version = 1
[filters.demo]
match_command = "^demo"
%s
strip_lines_matching = ["^NOISE"]

[[tests.demo]]
name = "stderr diagnostics are filtered"
stderr = "NOISE one two three four five\nNOISE six seven eight nine ten\nkeep this signal line"
min_saved_percent = 30
`
	run := func(flag string) TestOutcome {
		res, err := runTests(fmt.Sprintf(tmpl, flag), "")
		if err != nil {
			t.Fatalf("runTests(%s): %v", flag, err)
		}
		if len(res.Outcomes) != 1 {
			t.Fatalf("%s: got %d outcomes, want 1", flag, len(res.Outcomes))
		}
		return res.Outcomes[0]
	}

	if got := run("filter_stderr = true"); !got.Passed {
		t.Errorf("with filter_stderr the stderr-stripping test must pass; actual=%q", got.Actual)
	}
	if got := run("filter_stderr = false"); got.Passed {
		t.Errorf("without filter_stderr the savings floor MUST fail (the guard); actual=%q", got.Actual)
	}
}

// TestSingleStreamFixtureStillExactMatches guards back-compat: a classic
// single-stream `input`/`expected` test is unaffected by the split-stream path.
func TestSingleStreamFixtureStillExactMatches(t *testing.T) {
	content := `schema_version = 1
[filters.demo]
match_command = "^demo"
strip_lines_matching = ["^drop"]

[[tests.demo]]
name = "classic"
input = "drop me\nkeep me"
expected = "keep me"
`
	res, err := runTests(content, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outcomes) != 1 || !res.Outcomes[0].Passed {
		t.Errorf("classic single-stream test must still pass: %+v", res.Outcomes)
	}
}
