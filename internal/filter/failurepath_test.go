package filter

import (
	"testing"

	"github.com/BurntSushi/toml"
)

// failurePathFixtureGrandfather lists answer-droppable built-in filters that do
// not yet carry a failed=true fixture because their tool is NOT locally runnable
// here, so a real failure sample cannot be captured without blind-building one
// (which this repo forbids). Each entry names the tool and the reason. BURN THE
// LIST DOWN: when you next run the tool for real, capture a failed run, add a
// failed=true fixture proving the error survives, then delete the entry.
//
// The guard below makes the "answer-droppable filter with no failure-path test"
// gap impossible to GROW: any NEW filter with on_empty / keep_lines_matching /
// match_output must ship a failed=true fixture or be added here with a reason.
var failurePathFixtureGrandfather = map[string]string{
	// Python tooling - not installed here.
	"basedpyright": "basedpyright (Python type checker) not locally runnable; capture a real failed run",
	"flake8":       "flake8 (Python linter) not locally runnable; capture a real failed run",
	"ty":           "ty (Astral Python type checker) not locally runnable; capture a real failed run",
	// JS/TS tooling - not installed here.
	"biome":       "biome (JS/TS linter/formatter) not locally runnable; has filter_stderr, needs a regression fixture",
	"oxlint":      "oxlint (JS/TS linter) not locally runnable; capture a real failed run",
	"turbo":       "turbo (Turborepo) not locally runnable; capture a real failed run",
	"trunk-build": "trunk (trunk.io build) not locally runnable; capture a real failed run",
	// JVM build tooling - not installed here.
	"gradle":      "gradle (JVM build) not locally runnable; capture a real failed build",
	"mvn-build":   "mvn (Maven build) not locally runnable; capture a real failed build",
	"spring-boot": "spring-boot (Maven/Gradle plugin) not locally runnable; capture a real failed run",
	// Elixir tooling - not installed here.
	"mix-compile": "mix compile (Elixir) not locally runnable; capture a real failed compile",
	"mix-format":  "mix format (Elixir) not locally runnable; capture a real failed run",
	// Infra / IaC tooling - not installed here.
	"terraform-plan": "terraform plan not locally runnable; capture a real failed plan",
	"tofu-init":      "tofu init (OpenTofu) not locally runnable; capture a real failed init",
	"tofu-plan":      "tofu plan (OpenTofu) not locally runnable; capture a real failed plan",
	"tofu-fmt":       "tofu fmt (OpenTofu) not locally runnable; capture a real failed run",
	"liquibase":      "liquibase (DB migrations) not locally runnable; capture a real failed run",
	"skopeo":         "skopeo (container images) not locally runnable; capture a real failed run",
	// Misc tooling - not installed here.
	"mise":          "mise (dev tool version manager) not locally runnable; capture a real failed run",
	"task":          "task (go-task runner) not locally runnable; capture a real failed run",
	"pio-run":       "pio run (PlatformIO) not locally runnable; capture a real failed run",
	"shopify-theme": "shopify theme (Shopify CLI) not locally runnable; capture a real failed run",
	// Installed, but needs a real project to produce a genuine failure.
	"xcodebuild": "xcodebuild installed but needs a real Xcode project; capture a failed build to pin the fixture",
}

// TestAnswerDroppableFiltersHaveFailurePathFixture is the structural correctness
// guard from the 2026-06-24 filter audit. A filter that can SYNTHESIZE success
// (on_empty / match_output) or DROP non-matching lines (keep_lines_matching) can,
// if its strip/collapse logic is wrong, hide a failure behind a "looks ok"
// summary. The runner suppresses synthetic success on a non-zero exit, and a
// failed=true fixture pins that protection at the conformance layer. This test
// requires every answer-droppable built-in to carry such a fixture (or a
// grandfather entry), mirroring TestBuiltinFilterCount and the split-stream guard.
func TestAnswerDroppableFiltersHaveFailurePathFixture(t *testing.T) {
	content, err := concatBuiltins()
	if err != nil {
		t.Fatalf("concatBuiltins: %v", err)
	}
	var file tomlFile
	if _, err := toml.Decode(content, &file); err != nil {
		t.Fatalf("decode builtins: %v", err)
	}

	for name, f := range file.Filters {
		droppable := len(f.MatchOutput) > 0 || len(f.KeepLinesMatching) > 0 || f.OnEmpty != nil
		hasFailed := false
		for _, tc := range file.Tests[name] {
			if tc.Failed {
				hasFailed = true
				break
			}
		}
		_, grandfathered := failurePathFixtureGrandfather[name]

		if droppable && !hasFailed && !grandfathered {
			t.Errorf("filter %q can synthesize success / drop output (on_empty/keep_lines_matching/match_output) "+
				"but has no failed=true fixture. Add one proving a failed run is never faked to a success "+
				"message, or grandfather it in failurePathFixtureGrandfather with the tool + reason.", name)
		}
		// Burn-down: a grandfathered filter that now has its fixture (or no longer
		// drops output) must be removed from the list so it never goes stale.
		if grandfathered && hasFailed {
			t.Errorf("filter %q now has a failed=true fixture; remove it from failurePathFixtureGrandfather.", name)
		}
		if grandfathered && !droppable {
			t.Errorf("filter %q no longer drops/collapses output; remove it from failurePathFixtureGrandfather.", name)
		}
	}

	// A grandfather entry that names a non-existent filter is stale.
	for name := range failurePathFixtureGrandfather {
		if _, ok := file.Filters[name]; !ok {
			t.Errorf("failurePathFixtureGrandfather names %q, which is not a built-in filter; remove it.", name)
		}
	}
}
