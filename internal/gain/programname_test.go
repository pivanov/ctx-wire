package gain

import "testing"

// TestProgramNamePeelsRunnerPrefix pins that gain attributes a runner-prefixed
// command to the real tool (bunx prettier -> prettier), not the wrapper, while
// keeping the wrapper for bare runners and flag-first inner commands. It mirrors
// the {{runner}} and {{py-runner}} sets that filters match, so attribution and
// filtering stay in sync.
func TestProgramNamePeelsRunnerPrefix(t *testing.T) {
	cases := []struct{ cmd, want string }{
		// {{runner}} set , peel to the real tool.
		{"bunx prettier --check .", "prettier"},
		{"npx eslint .", "eslint"},
		{"pnpm dlx tsc --noEmit", "tsc"},
		{"yarn dlx some-tool", "some-tool"},
		{"pnpm exec vitest run", "vitest"},
		{"yarn exec jest", "jest"},
		{"bun x tsc", "tsc"},
		// {{py-runner}} set.
		{"poetry run pytest -q", "pytest"},
		{"uv run mypy .", "mypy"},
		{"uvx ruff check .", "ruff"},
		{"uv tool run black .", "black"},
		{"pipenv run flake8", "flake8"},
		{"python -m pytest", "pytest"},
		{"python3 -m flake8 .", "flake8"},
		// Not peeled / guarded.
		{"bunx", "bunx"},                  // no inner tool
		{"npx -y create-vite app", "npx"}, // inner is a flag, keep the wrapper
		{"git status", "git"},             // not a runner
		{"python script.py", "python"},    // python runs a script, not -m
		{"/usr/local/bin/rg foo", "rg"},   // path basename, no runner
		{"(none)", "(none)"},
	}
	for _, c := range cases {
		if got := programName(c.cmd); got != c.want {
			t.Errorf("programName(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}
