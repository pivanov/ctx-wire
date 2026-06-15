package filter

import (
	"strings"
	"testing"
)

// builtinFilterCount is the number of built-in filter definitions.
// Update this when filters are added or removed under filters/.
const builtinFilterCount = 143

// TestBuiltinConformance runs every inline [[tests.*]] case shipped with the
// built-in filters and asserts each filter's expected output. These inline
// cases are the conformance oracle for the pipeline.
func TestBuiltinConformance(t *testing.T) {
	res, err := VerifyBuiltin("")
	if err != nil {
		t.Fatalf("VerifyBuiltin: %v", err)
	}
	if len(res.Outcomes) == 0 {
		t.Fatal("no inline tests ran; expected the vendored conformance suite")
	}
	for _, o := range res.Outcomes {
		o := o
		t.Run(o.FilterName+"/"+o.TestName, func(t *testing.T) {
			if !o.Passed {
				t.Errorf("output mismatch\n expected: %q\n actual:   %q", o.Expected, o.Actual)
			}
		})
	}
}

// TestVerifyUnknownFilter ensures requesting a nonexistent filter is an error,
// not a vacuous success.
func TestVerifyUnknownFilter(t *testing.T) {
	if _, err := VerifyBuiltin("definitely-not-a-real-filter"); err == nil {
		t.Error("expected error for unknown filter, got nil")
	}
}

// TestBuiltinFilterCount guards against accidentally adding or dropping a
// vendored filter file.
func TestBuiltinFilterCount(t *testing.T) {
	names, err := builtinFilterNames()
	if err != nil {
		t.Fatalf("builtinFilterNames: %v", err)
	}
	if len(names) != builtinFilterCount {
		t.Errorf("got %d built-in filters, want %d: %v", len(names), builtinFilterCount, names)
	}
}

// TestAllBuiltinFiltersHaveTests ensures no filter ships without coverage.
func TestAllBuiltinFiltersHaveTests(t *testing.T) {
	res, err := VerifyBuiltin("")
	if err != nil {
		t.Fatalf("VerifyBuiltin: %v", err)
	}
	if len(res.FiltersWithoutTest) > 0 {
		t.Errorf("filters without inline tests: %v", res.FiltersWithoutTest)
	}
}

// TestRegistryFind checks that LoadBuiltin compiles and matching works.
// TestRunnerPrefixConsistency is the drift guard for the shared {{runner}}
// token: every runner-able tool must route to its filter through EVERY launch
// form. A filter that hand-rolls a partial prefix, or a new runner-able tool
// that forgets the token, fails here instead of silently going unfiltered. The
// cross-product makes "the prefix is consistent" a CI fact, not a hope, and is
// what caught js-test/playwright lagging behind biome/eslint/tsc/pyright.
func TestRunnerPrefixConsistency(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	tools := []struct{ invoke, filter string }{
		{"biome check .", "biome"},
		{"eslint .", "eslint"},
		{"tsc --noEmit", "tsc"},
		{"pyright src", "pyright"},
		{"vitest run", "js-test"},
		{"jest", "js-test"},
		{"playwright install chromium", "playwright-install"},
	}
	// Bare invocation plus every launch form the {{runner}} token must cover.
	runners := []string{"", "npx ", "bunx ", "pnpm dlx ", "yarn dlx ", "pnpm exec ", "yarn exec ", "bun x "}
	for _, tool := range tools {
		for _, r := range runners {
			cmd := r + tool.invoke
			got := reg.Find(cmd)
			if got == nil {
				t.Errorf("Find(%q) = passthrough, want %q (runner-prefix drift?)", cmd, tool.filter)
				continue
			}
			if got.Name != tool.filter {
				t.Errorf("Find(%q) = %q, want %q", cmd, got.Name, tool.filter)
			}
		}
	}
}

// TestPyRunnerPrefixConsistency pins that every Python-cohort filter accepts the
// full {{py-runner}} prefix set, so `poetry run pytest` (and pipenv/pdm/hatch/rye
// run, plus `python -m`) is filtered rather than passed through. Guards the drift
// this token replaced: each filter used to hand-roll only `uv run` and silently
// missed the far more common `poetry run`.
func TestPyRunnerPrefixConsistency(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	tools := []struct{ invoke, filter string }{
		{"pytest -q", "pytest"},
		{"ruff check .", "ruff"},
		{"mypy .", "mypy"},
		{"pylint app", "pylint"},
		{"flake8 .", "flake8"},
		{"pyright src", "pyright"},
		{"basedpyright src", "basedpyright"},
	}
	// Bare invocation plus every prefix form the {{py-runner}} token must cover.
	prefixes := []string{"", "uv run ", "poetry run ", "pipenv run ", "pdm run ", "hatch run ", "rye run ", "python -m ", "python3 -m "}
	for _, tool := range tools {
		for _, p := range prefixes {
			cmd := p + tool.invoke
			got := reg.Find(cmd)
			if got == nil {
				t.Errorf("Find(%q) = passthrough, want %q (py-runner-prefix drift?)", cmd, tool.filter)
				continue
			}
			if got.Name != tool.filter {
				t.Errorf("Find(%q) = %q, want %q", cmd, got.Name, tool.filter)
			}
		}
	}
}

func TestRegistryFind(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	tests := []struct {
		name    string
		command string
		want    string // matched filter name, "" for passthrough
	}{
		{"dotnet build matches", "dotnet build -c Release", "dotnet-build"},
		{"make matches", "make all", "make"},
		{"terraform plan matches", "terraform plan -out=tfplan", "terraform-plan"},
		{"unknown passes through", "some-unknown-tool --flag", ""},
		// Real command strings the matching used to miss.
		{"gradle build matches", "gradle build", "gradle"},
		{"gradlew build matches", "gradlew build", "gradle"},
		{"./gradlew matches", "./gradlew assemble", "gradle"},
		{"g++ matches", "g++ -O2 main.cpp", "gcc"},
		{"gcc matches", "gcc -c foo.c", "gcc"},
		{"git status matches", "git status --short", "git-status"},
		{"git -C status matches", "git -C /Users/pivanov/workspace/repo status --short --branch", "git-status"},
		{"rg matches", "rg TODO .", "rg"},
		{"ripgrep matches", "ripgrep TODO .", "rg"},
		{"grep matches", "grep -R TODO .", "grep"},
		{"git grep matches", "git grep TODO", "grep"},
		{"ls matches", "ls -la", "ls"},
		{"lsof matches", "lsof -i :7222 -n -P", "lsof"},
		{"find matches", "find . -name '*.go'", "find"},
		{"tree matches", "tree -L 2", "tree"},
		{"env matches", "env", "env"},
		{"env assignment listing matches", "env FOO=bar", "env"},
		{"env wrapper does not match", "env FOO=bar go test", ""},
		{"printenv matches", "printenv PATH", "printenv"},
		{"cat matches", "cat README.md", "cat"},
		{"sed matches", "sed -n '1,10p' README.md", "sed"},
		{"full path sed matches", "/usr/bin/sed -n '1,10p' README.md", "sed"},
		{"head matches", "head README.md", "head"},
		{"tail matches", "tail README.md", "tail"},
		{"nl matches", "nl -ba README.md", "nl"},
		{"cargo build matches", "cargo build --release", "cargo"},
		{"cargo +toolchain build matches", "cargo +nightly build", "cargo"},
		{"cargo +toolchain test matches", "cargo +stable test --workspace", "cargo"},
		{"cargo run still passes through (avoids eating program stdout)", "cargo run --bin app", ""},
		{"git diff matches", "git diff -- README.md", "git-diff"},
		{"git -C diff matches", "git -C /tmp/repo diff -- README.md", "git-diff"},
		{"git show matches", "git show HEAD", "git-diff"},
		{"git log matches", "git log --oneline", "git-log"},
		{"git -c log matches", "git -c core.quotepath=false log --oneline", "git-log"},
		{"npm install matches", "npm install", "node-package"},
		{"npm run build matches", "npm run build", "node-build"},
		{"npm run arbitrary colon script matches package-script", "npm run build:storybook", "package-script"},
		{"pnpm build matches", "pnpm build", "node-build"},
		{"pnpm run arbitrary colon script matches package-script", "pnpm run check:types", "package-script"},
		{"yarn arbitrary colon script matches package-script", "yarn storybook:build", "package-script"},
		{"jest matches", "jest --runInBand", "js-test"},
		{"vitest matches", "vitest run", "js-test"},
		{"tsc matches", "tsc --noEmit", "tsc"},
		{"npm run typecheck matches", "npm run typecheck", "tsc"},
		{"eslint matches", "eslint .", "eslint"},
		{"npm run lint matches", "npm run lint", "eslint"},
		{"bun lint matches", "bun lint", "bun"},
		{"workspace bun lint matches", "bun --filter @edyn/web-components lint", "bun"},
		{"bun eval matches inline-script", "bun -e 'console.log(1)'", "inline-script"},
		{"node eval matches inline-script", "node --eval 'console.log(1)'", "inline-script"},
		{"bash script matches shell-script", "bash /tmp/inv.sh", "shell-script"},
		{"sh script matches shell-script", "sh -- /tmp/inv.sh", "shell-script"},
		{"agent-browser matches", "agent-browser eval 'document.title'", "agent-browser"},
		{"biome check matches", "biome check .", "biome"},
		{"npx biome matches biome", "npx biome check .", "biome"},
		{"bunx biome matches biome", "bunx biome check .", "biome"},
		{"pnpm dlx biome matches biome", "pnpm dlx biome check .", "biome"},
		{"yarn dlx biome matches biome", "yarn dlx biome check .", "biome"},
		// Exhaustive runner-form coverage for all runner-able tools lives in
		// TestRunnerPrefixConsistency (the {{runner}} drift guard).
		{"npx playwright install prefers playwright", "npx playwright install chromium", "playwright-install"},
		{"strings matches", "strings /tmp/file.bin", "strings"},
		{"smoke script path matches", "scripts/smoke.sh", "smoke-sh"},
		{"cargo test matches", "cargo test", "cargo"},
		{"go test matches", "go test ./...", "go"},
		{"full path go test matches", "/usr/local/go/bin/go test ./...", "go"},
		{"pytest matches", "pytest -q", "pytest"},
		{"ruff matches", "ruff check .", "ruff"},
		{"mypy matches", "mypy .", "mypy"},
		{"pip install matches", "pip install requests", "pip-install"},
		{"docker build matches", "docker build .", "docker"},
		{"kubectl matches", "kubectl apply -f deploy.yaml", "kubectl"},
		{"gh pr list prefers specific filter", "gh pr list", "gh-pr"},
		{"apt-get matches", "apt-get install jq", "apt"},
		{"dotnet test matches", "dotnet test", "dotnet-test"},
		{"mvn test matches", "mvn test", "mvn-test"},
		{"gradle test matches", "gradle test", "gradle-test"},
		// Specificity: spring-boot's precise pattern must win over broad gradle.
		{"gradle bootRun prefers spring-boot", "gradle clean bootRun", "spring-boot"},
		// Batch D: new coverage.
		{"go mod tidy matches go-mod-tidy", "go mod tidy", "go-mod-tidy"},
		{"go mod download matches go-mod-download", "go mod download", "go-mod-download"},
		{"go mod why matches go-mod-why", "go mod why example.com/x", "go-mod-why"},
		{"go list matches go-list", "go list ./...", "go-list"},
		{"git branch matches git-list", "git branch -a", "git-list"},
		{"git ls-files matches git-list", "git ls-files", "git-list"},
		{"pip freeze matches pip-list", "pip freeze", "pip-list"},
		{"sort matches sort", "sort big.txt", "sort"},
		{"uniq matches uniq", "uniq -c", "uniq"},
		{"terraform output matches terraform-show", "terraform output", "terraform-show"},
		{"tofu state list matches tofu-show", "tofu state list", "tofu-show"},
		// Batch D: uv run prefix broadening.
		{"uv run pytest matches pytest", "uv run pytest -q", "pytest"},
		{"uv run ruff matches ruff", "uv run ruff check .", "ruff"},
		{"uv run mypy matches mypy", "uv run mypy .", "mypy"},
		// {{py-runner}}: poetry/pipenv/pdm/hatch/rye run, not just uv (exhaustive
		// coverage in TestPyRunnerPrefixConsistency).
		{"poetry run pytest matches pytest", "poetry run pytest -q", "pytest"},
		{"pipenv run ruff matches ruff", "pipenv run ruff check .", "ruff"},
		{"pdm run mypy matches mypy", "pdm run mypy .", "mypy"},
		// Batch D fix: JSON modes must route to passthrough guards, not truncating filters.
		{"go list -json routes to guard", "go list -json ./...", "go-list-json"},
		{"terraform show -json routes to guard", "terraform show -json plan.out", "terraform-show-json"},
		{"terraform output -json routes to guard", "terraform output -json", "terraform-show-json"},
		{"tofu show -json routes to guard", "tofu show -json", "tofu-show-json"},
		{"tofu output -json routes to guard", "tofu output -json", "tofu-show-json"},
		// Tier-1 coverage expansion.
		{"bun test matches bun", "bun test", "bun"},
		{"bun workspace storybook matches bun", "bun --filter @edyn/web-components build-storybook", "bun"},
		{"bun arbitrary colon script matches package-script", "bun build:storybook", "package-script"},
		{"bun workspace arbitrary colon script matches package-script", "bun --filter @edyn/web-components build:storybook", "package-script"},
		{"deno lint matches deno", "deno lint", "deno"},
		{"npm audit matches node-audit", "npm audit", "node-audit"},
		{"pnpm list matches node-list", "pnpm list --depth 0", "node-list"},
		{"pyright matches pyright", "pyright .", "pyright"},
		{"uv run pylint matches pylint", "uv run pylint app", "pylint"},
		{"flake8 matches flake8", "flake8 .", "flake8"},
		{"golangci-lint matches", "golangci-lint run", "golangci-lint"},
		{"curl matches curl", "curl -sS http://localhost:3000/health", "curl"},
		{"wget matches wget", "wget https://example.com/file.tgz", "wget"},
		{"httpie matches httpie", "http GET :3000/health", "httpie"},
		{"brew list matches brew-list", "brew list", "brew-list"},
		{"docker ps prefers docker-list", "docker ps", "docker-list"},
		{"docker logs prefers docker-logs", "docker logs web", "docker-logs"},
		{"eza matches modern-ls", "eza -la", "modern-ls"},
		{"ag matches search-tools", "ag TODO .", "search-tools"},
		{"python unittest matches", "python -m unittest discover", "python-unittest"},
		{"bundle exec rspec matches", "bundle exec rspec", "rspec"},
		{"phpunit matches", "vendor/bin/phpunit", "phpunit"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := reg.Find(tt.command)
			switch {
			case tt.want == "" && got != nil:
				t.Errorf("Find(%q) = %q, want passthrough", tt.command, got.Name)
			case tt.want != "" && got == nil:
				t.Errorf("Find(%q) = passthrough, want %q", tt.command, tt.want)
			case tt.want != "" && got.Name != tt.want:
				t.Errorf("Find(%q) = %q, want %q", tt.command, got.Name, tt.want)
			}
		})
	}
}

func TestCatTruncatesHugeSingleLinePayload(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	f := reg.Find("cat /Users/alice/.claude/projects/session.jsonl")
	if f == nil || f.Name != "cat" {
		t.Fatalf("cat command matched %v, want cat", f)
	}
	in := `{"type":"message","content":"` + strings.Repeat("x", 2000) + `"}`
	got := ApplyWithMeta(f, in)
	if !got.Truncated {
		t.Fatal("expected long single-line cat payload to be marked truncated")
	}
	if len(got.Output) > 520 {
		t.Fatalf("cat output length = %d, want around 500", len(got.Output))
	}
	if !strings.HasSuffix(got.Output, "...") {
		t.Fatalf("truncated output should end with ellipsis: %q", got.Output)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{
		{"under limit unchanged", "hello", 5, "hello"},
		{"ascii truncated", "hello world", 8, "hello..."},
		{"unicode rune-counted", "日本語xyz", 5, "日本..."},
		{"maxLen below 3", "abcdef", 2, "..."},
		{"empty stays empty", "", 5, ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.maxLen); got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty yields nothing", "", nil},
		{"single line no newline", "a", []string{"a"}},
		{"trailing newline dropped", "a\n", []string{"a"}},
		{"two lines", "a\nb", []string{"a", "b"}},
		{"crlf stripped when terminated", "a\r\n", []string{"a"}},
		{"crlf interior stripped", "a\r\nb", []string{"a", "b"}},
		{"lone cr final kept", "a\r", []string{"a\r"}},
		{"bare newline yields empty line", "\n", []string{""}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines(tt.in)
			if !equalStrings(got, tt.want) {
				t.Errorf("splitLines(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

// TestApplyStages exercises the pipeline primitives with small inline filters,
// one subtest per stage.
func TestApplyStages(t *testing.T) {
	tests := []struct {
		name  string
		toml  string
		input string
		want  string
	}{
		{
			name:  "strip_ansi",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nstrip_ansi=true\n",
			input: "\x1b[31mError\x1b[0m\nnormal",
			want:  "Error\nnormal",
		},
		{
			name:  "strip_ansi_private_modes",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nstrip_ansi=true\n",
			input: "\x1b[?25lhide cursor\x1b[?25h\n\x1b[?1049halt screen\x1b[?1049l",
			want:  "hide cursor\nalt screen",
		},
		{
			name:  "strip_ansi_osc_sequences",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nstrip_ansi=true\n",
			input: "start \x1b]8;;https://example.test\x07link\x1b]8;;\x07 end\n\x1b]0;title\x1b\\body",
			want:  "start link end\nbody",
		},
		{
			name:  "strip_lines_matching",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nstrip_lines_matching=[\"^noise\"]\n",
			input: "noise line\nkeep this",
			want:  "keep this",
		},
		{
			name:  "keep_lines_matching",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nkeep_lines_matching=[\"^PASS\"]\n",
			input: "PASS a\nnoise\nPASS b",
			want:  "PASS a\nPASS b",
		},
		{
			name:  "head_lines omit message",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nhead_lines=2\n",
			input: "a\nb\nc\nd\ne",
			want:  "a\nb\n... (3 lines omitted)",
		},
		{
			name:  "tail_lines omit message",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\ntail_lines=2\n",
			input: "a\nb\nc\nd\ne",
			want:  "... (3 lines omitted)\nd\ne",
		},
		{
			name:  "head and tail combined",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nhead_lines=2\ntail_lines=2\n",
			input: "a\nb\nc\nd\ne\nf",
			want:  "a\nb\n... (2 lines omitted)\ne\nf",
		},
		{
			name:  "max_lines counts truncated message",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nmax_lines=3\n",
			input: "a\nb\nc\nd\ne",
			want:  "a\nb\nc\n... (2 lines truncated)",
		},
		{
			name:  "on_empty fires when all stripped",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nstrip_lines_matching=[\".*\"]\non_empty=\"nothing left\"\n",
			input: "line1\nline2",
			want:  "nothing left",
		},
		{
			name:  "replace chained sequentially",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nreplace=[{pattern=\"aaa\",replacement=\"bbb\"},{pattern=\"bbb\",replacement=\"ccc\"}]\n",
			input: "aaa",
			want:  "ccc",
		},
		{
			name:  "replace backreference",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nreplace=[{pattern=\"(\\\\w+):(\\\\w+)\",replacement=\"$2:$1\"}]\n",
			input: "hello:world",
			want:  "world:hello",
		},
		{
			name:  "match_output short circuit",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nmatch_output=[{pattern=\"Switched to branch\",message=\"ok\"}]\n",
			input: "Switched to branch 'main'",
			want:  "ok",
		},
		{
			name:  "match_output unless blocks on error",
			toml:  "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nmatch_output=[{pattern=\"total size is\",message=\"ok\",unless=\"error|failed\"}]\n",
			input: "rsync: error\ntotal size is 1000",
			want:  "rsync: error\ntotal size is 1000",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cf := firstFilter(t, tt.toml)
			if got := Apply(cf, tt.input); got != tt.want {
				t.Errorf("Apply mismatch\n input:    %q\n expected: %q\n actual:   %q", tt.input, tt.want, got)
			}
		})
	}
}

func TestCompileRejectsBadFilters(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{"strip and keep mutually exclusive", "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nstrip_lines_matching=[\"a\"]\nkeep_lines_matching=[\"b\"]\n"},
		{"invalid match_command", "schema_version=1\n[filters.f]\nmatch_command=\"[\"\n"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAndCompile(tt.toml, "test")
			if err != nil {
				return // a hard parse error is also acceptable rejection
			}
			if len(got) != 0 {
				t.Errorf("expected bad filter to be skipped, got %d compiled", len(got))
			}
		})
	}
}

func TestApplyWithMetaReportsTruncation(t *testing.T) {
	tests := []struct {
		name string
		toml string
		in   string
		want bool
	}{
		{
			name: "max_lines",
			toml: "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nmax_lines=1\n",
			in:   "a\nb",
			want: true,
		},
		{
			name: "truncate_lines_at",
			toml: "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\ntruncate_lines_at=5\n",
			in:   "abcdef",
			want: true,
		},
		{
			name: "head_lines",
			toml: "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nhead_lines=1\n",
			in:   "a\nb",
			want: true,
		},
		{
			name: "tail_lines",
			toml: "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\ntail_lines=1\n",
			in:   "a\nb",
			want: true,
		},
		{
			name: "strip_lines_not_recovery_truncation",
			toml: "schema_version=1\n[filters.f]\nmatch_command=\"^cmd\"\nstrip_lines_matching=[\"^noise\"]\n",
			in:   "noise\nkeep",
			want: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cf := firstFilter(t, tt.toml)
			got := ApplyWithMeta(cf, tt.in)
			if got.Truncated != tt.want {
				t.Errorf("Truncated = %v, want %v (output %q)", got.Truncated, tt.want, got.Output)
			}
		})
	}
}

// firstFilter compiles inline TOML and returns its single filter.
func TestReduceJSONFlag(t *testing.T) {
	on := firstFilter(t, `
schema_version = 1
[filters.j]
match_command = "^jq\\b"
reduce_json = true
`)
	if !on.ReducesJSON() {
		t.Error("reduce_json = true should set ReducesJSON()")
	}
	off := firstFilter(t, `
schema_version = 1
[filters.c]
match_command = "^cat\\b"
truncate_lines_at = 500
`)
	if off.ReducesJSON() {
		t.Error("ReducesJSON() should default to false")
	}
}

func firstFilter(t *testing.T, content string) *CompiledFilter {
	t.Helper()
	got, err := parseAndCompile(content, "test")
	if err != nil {
		t.Fatalf("parseAndCompile: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one filter, got %d", len(got))
	}
	return got[0]
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
