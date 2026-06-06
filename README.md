# ctx-wire

A small Go binary that sits between AI coding agents and the noisy command
output they pay tokens to read. It runs a command, compresses the output with
declarative filters, scrubs secrets, and hands the agent a short result while
keeping the full (scrubbed) log on disk when something fails.

Status: **released**. The Go test suite covers the feature surface,
`scripts/smoke.sh` exercises the main end-to-end paths, and `ctx-wire verify`
checks 142 built-in filters with 326 conformance tests.

## Install

```sh
curl -fsSL https://ctx-wire.dev/install.sh | sh
```

Downloads the latest release binary for your OS/arch into `~/.local/bin`. Then
wire up your agent and watch the savings:

```sh
ctx-wire init claude   # or codex, cursor, gemini, copilot, ...
ctx-wire gain
```

Upgrade later with `ctx-wire update`. macOS and Linux. Windows: download a `.zip`
from the [releases](https://github.com/pivanov/ctx-wire/releases). Building from
source is below.

## Build

```sh
go build -o ctx-wire ./cmd/ctx-wire
```

With version metadata (what releases use):

```sh
go build -ldflags "-X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
  -o ctx-wire ./cmd/ctx-wire
```

`ctx-wire version` prints the injected version, commit, and build date.

## Package

Build a shareable archive under `dist/`:

```sh
VERSION=0.1.0-rc1 just pack
```

This writes `dist/ctx-wire_<version>_<os>_<arch>.tar.gz` plus a `.sha256`
checksum. The archive contains the `ctx-wire` binary with executable
permissions, `README.md`, and `INSTALL.txt`.

For a downloaded macOS archive, remove the quarantine attribute before the first
run if Gatekeeper offers only "Move to Trash":

```sh
xattr -d com.apple.quarantine ./ctx-wire 2>/dev/null || true
./ctx-wire init claude    # or codex, cursor, gemini, ...
```

## Smoke test

End-to-end check of the main install, run, hook, MCP, trust, gain, tune, and
telemetry paths. It builds the binary and runs in hermetic temp directories, so
it never touches your real HOME, agent config, or telemetry:

```sh
bash scripts/smoke.sh
```

Exits non-zero if any check fails.

## Developer commands

Common development tasks run through [`just`](https://github.com/casey/just),
similar to scripts in a `package.json`:

```sh
just            # list recipes
```

| Command | What it runs |
|---|---|
| `just build` | Build a local `ctx-wire` binary |
| `VERSION=1.0.0 just build-release` | Build with version, commit, and date metadata |
| `VERSION=1.0.0 just pack` | Build a release archive and checksum under `dist/` |
| `just pack-all` | Build release archives for every platform under `dist/` |
| `just fmt-check` | Fail if Go files need `gofmt` |
| `just test` | Run `go test ./...` |
| `just race` | Run `go test -race ./...` |
| `just vet` | Run `go vet ./...` |
| `just verify` | Run `ctx-wire verify` through `go run` |
| `just smoke` | Run `scripts/smoke.sh` |
| `just check` | Run format check, vet, tests, race tests, and verify |
| `just rc` | Run `just check` plus the smoke suite |
| `just clean` | Remove the local build output |

## Commands

| Command | What it does |
|---|---|
| `ctx-wire run <cmd> [args]` | Execute a command and filter/scrub its output |
| `ctx-wire mcp` | Serve `run_command` and `read_file` filtering tools over MCP (stdio) |
| `ctx-wire hook <agent>` | Run as an agent pre-tool hook (reads JSON on stdin) |
| `ctx-wire rewrite <line>` | Print the rewritten form of a shell command line |
| `ctx-wire init <agent>` | Install the binary into `~/.local/bin`, add managed shims, and wire an agent (claude, cursor, codex, gemini, cline, windsurf, kilocode, antigravity, opencode, pi, hermes, copilot, vscode, visualstudio) |
| `ctx-wire update [--check]` | Upgrade to the latest release (checksum-verified, atomic, with rollback); `--check` only reports |
| `ctx-wire uninstall` | Remove the ctx-wire binary, managed shims, and only ctx-wire hook/config entries |
| `ctx-wire trust` | Approve this project's `.ctx-wire/filters.toml` by hash |
| `ctx-wire untrust` | Revoke trust for this project's `.ctx-wire/filters.toml` |
| `ctx-wire gain` | Report token savings recorded so far |
| `ctx-wire gain --since 1h` | Report only recent savings |
| `ctx-wire gain --history [--top N]` | Recent commands, newest last (optionally cap to N) |
| `ctx-wire gain --daily \| --weekly \| --monthly` | Savings grouped by period |
| `ctx-wire gain --graph` | ASCII bar graph of daily saved bytes |
| `ctx-wire gain --json \| --csv` | Export the summary/daily breakdown |
| `ctx-wire gain --quota [--budget <tokens>] [--window <tokens>]` | Month-to-date savings vs a vendor-neutral token budget, with a per-agent split |
| `ctx-wire gain clear` | Clear local gain history for a fresh dogfood window |
| `ctx-wire explain <cmd>` | Diagnose how ctx-wire handles one command (filter, mode, hook) |
| `ctx-wire tune [--since 24h] [--top N]` | Higher-level filter improvement report from gain data (read-only) |
| `ctx-wire discover [--since 24h] [--top N] [--all]` | Find agent commands (Claude/Codex transcripts) that escaped ctx-wire (read-only) |
| `ctx-wire learn [--since D] [--all] [--min N] [--write]` | Mine Claude transcripts for failed->corrected commands; `--write` saves `.claude/rules/cli-corrections.md` |
| `ctx-wire session [--since 24h] [--top N] [--all]` | Per-session ctx-wire adoption across agent transcripts (read-only) |
| `ctx-wire tune preview` | Dry-run the sanitized bundle contents without writing files |
| `ctx-wire tune bundle [--out PATH]` | Write a sanitized tune bundle archive for manual sharing |
| `ctx-wire tune issue [--open]` | Print or open a sanitized GitHub issue draft |
| `ctx-wire telemetry [status\|enable\|disable\|forget]` | Show or change anonymous aggregate telemetry status; `forget` withdraws consent and erases local data |
| `ctx-wire doctor [--recent N]` | Check install/hooks/MCP/storage/trust health (read-only) |
| `ctx-wire verify [filter]` | Run the built-in filter conformance tests |
| `ctx-wire version` | Print version and build metadata |

### `ctx-wire explain`

`explain` is diagnostic only and never changes anything.

- `ctx-wire explain <cmd>` shows, without running it: whether the hook would
  **wrap** the command in `ctx-wire run` or pass it through (and why: pipeline,
  redirection, shell builtin/keyword, env-assignment, subshell, already
  ctx-wire), and what the runner would then do (**filtered** by which filter,
  **live passthrough** when no filter matches, or **inherited bypass** for
  interactive/streaming commands).
The cross-command token-opportunity report (grouping the biggest gaps into
classes such as *missing filter*, *filtered but weak*, *common passthrough*, and
*expected payload*) lives in `ctx-wire tune`, not here, so `explain` stays a
single-command diagnostic. Running `ctx-wire explain` with no command prints its
help and points you to `tune`.

### `ctx-wire tune`

`tune` is read-only and local-only: it never runs commands, reads raw output,
captures samples, writes files, or makes network calls. It reads the recorded
gain data and reuses `explain`'s classification to print a higher-level filter
improvement report grouped into actionable sections:

- **Missing filters**: passthrough commands that should be filtered (add a new
  built-in filter, or broaden an existing one that did not match).
- **Weak filters**: filtered tooling commands that save little.
- **Payload commands**: source/search/diff/list output expected to stay large
  (reported as expected payload, not a bad filter).
- **Command-shape hints**: cross-cutting tips such as `rg`/`grep`/`ag`/`ack`
  without `-n`, an unscoped `find`, a command that repeats absolute paths, or
  a full ctx-wire tee log read that should use `head`, `tail`, or `sed -n`.

Pipeline/redirect/interactive passthroughs (which ctx-wire cannot wrap) and
non-actionable commands are acknowledged in footers rather than hidden. Use
`ctx-wire tune --since 24h` to window the report and `ctx-wire tune --top N` to
cap rows per section.

`ctx-wire tune preview` is a dry run of what `tune bundle` would contain. It
writes nothing, captures no raw command output, and makes no network calls. It
prints the bundle manifest, the sanitized sample commands, and the privacy
guarantees. The sanitizer reuses `scrub.Scrub`, replaces the user home with
`$HOME` and the current project root with `$PROJECT` (matching only at path
boundaries so names like `repository` are not mangled), compacts long absolute
paths (keeping the trailing segments), and caps the displayed command length.

`ctx-wire tune bundle [--out PATH]` writes a sanitized `.tar.gz` archive for
manual sharing (default `ctx-wire-tune.tar.gz`). It captures no raw command
output and makes no network calls; the only write is the archive itself, whose
path is printed with a reminder to inspect it before sharing. The archive
contains `summary.json` (counts, byte totals, window, version, OS/arch, filter
and conformance counts, top classes), `report.txt` (the sanitized tune report),
`suggestions.json`, `samples/commands.jsonl` (sanitized sample commands, one per
line), and `privacy_report.txt`. Every exported command runs through the same
sanitizer as `tune preview`.

`ctx-wire tune issue` prints a Markdown GitHub issue body (suggested title,
summary, top classes, suggestions, a privacy checklist, and bundle-attachment
instructions) built from the same sanitized data. By default it is fully
read-only: no files, no browser, no network. It supports `--since`, `--top`, and
`--bundle PATH` (which only mentions an existing bundle path; it does not create
one). `ctx-wire tune issue --open` opens GitHub's new-issue page with the title
and body prefilled in your browser so you can review and submit manually:
ctx-wire never calls the GitHub API, stores a token, or uploads anything. It
takes `--repo owner/repo`, or best-effort infers the repo from `git remote
get-url origin`; if the repo cannot be inferred or the prefilled URL would be too
long, it prints the issue body instead of opening a browser.

### `ctx-wire doctor`

`doctor` is read-only. It reports the running binary and whether `ctx-wire` on
your `PATH` is that same binary, whether managed command shims are installed and
first on `PATH`, whether any shim captures have been recorded, which agent
hooks/rules are installed (Claude, Cursor, Codex, Gemini, Cline, Windsurf,
Kilo Code, Antigravity, OpenCode, Pi, Hermes, Copilot, plus Codex's
hooks-feature flag), whether MCP config exists for VS Code / Visual Studio,
whether the gain and tee directories are writable (with the sandbox fallback),
and the project filter trust state. It prints counts only by default; pass
`--recent N` (or `--verbose`) to also list the last N scrubbed commands. Exit
code is `0` when healthy or only warnings, `1` for a broken install (unwritable
storage, unloadable filter registry).

### Integrations (`ctx-wire init <target>`)

- **self**: copies the current binary to `~/.local/bin/ctx-wire` with executable
  permissions and installs managed command shims next to it. The shims cover
  common agent commands such as `git`, `rg`, `grep`, `cat`, `sed`, `head`,
  `tail`, `sort`, `bun`, `npm`, `go`, and `cargo`. Existing non-ctx-wire files
  are never overwritten, and ctx-wire only creates a shim when the real command
  is already installed elsewhere on `PATH` (so it never makes `jq` or `git`
  appear to exist). Normal terminal commands bypass ctx-wire and exec the real
  tool directly, preserving native colors/progress behavior.
- **Claude Code, Cursor, Codex, Gemini CLI, Cline, Windsurf, Kilo Code,
  Antigravity, OpenCode, Pi, Hermes, Copilot, VS Code, Visual Studio**: each
  agent init also installs the local binary and managed
  command shims, so there is no separate shim step to remember. Each shim
  removes its own directory from `PATH`, resolves the real command, and then
  calls `ctx-wire run` with the real absolute path only when the process is
  agent-marked (`CTX_WIRE_AGENT_SHIMS=1` / `CTX_WIRE_SHIMS=1`) or launched from
  an agent-looking parent process. That avoids recursive shim calls and keeps
  your ordinary terminal path native. Shim captures are counted in a scrubbed
  local usage log so `ctx-wire doctor` can show whether they are actually being
  exercised. To exempt an agent-spawned helper whose stdout must stay byte-exact
  (a statusline command, a hook, an MCP subprocess), set
  `CTX_WIRE_DISABLE_SHIMS=1` at the top of that script: it is the first check in
  every shim, so the real command runs unwrapped. `ctx-wire run` already sets it
  for the commands it wraps, so nested pipelines and helpers under a wrapped
  command stay raw.
- **Claude Code, Cursor, Codex, Gemini CLI**: transparent command rewrite via a
  pre-tool hook. Codex additionally requires you to enable its hooks feature and
  trust the hook (the command prints the exact steps; it never bypasses trust).
  The Claude hook also respects your Bash permission rules: if a command matches
  a `permissions.deny` or `permissions.ask` rule in your `settings.json`,
  ctx-wire does **not** auto-approve the rewritten form. It steps aside so Claude
  applies its own decision to the original command, instead of the wrapper hiding
  the command from your deny/ask rules. Commands with no matching rule keep the
  transparent allow-and-filter behavior.
- **GitHub Copilot project integration**: writes `.github/copilot-instructions.md`
  plus `.github/hooks/ctx-wire-rewrite.json`. VS Code-style hook payloads are
  rewritten; Copilot CLI payloads get a deny-with-suggestion response because it
  does not expose the same rewrite contract.
- **Cline, Windsurf, Kilo Code, Antigravity**: prompt/rules guidance via
  `.clinerules`, `.windsurfrules`, `.kilocode/rules/ctx-wire-rules.md`, and
  `.agents/rules/antigravity-ctx-wire-rules.md`. These are not hook
  interception; they steer the agent to use `ctx-wire run`.
- **OpenCode, Pi, Hermes**: global plugin/extension files that call
  `ctx-wire rewrite` so shell commands are routed through ctx-wire when the host
  agent supports that plugin surface.
- **VS Code Copilot, Visual Studio Copilot**: an MCP server entry pointing at
  `ctx-wire mcp`.
- **Uninstall**: `ctx-wire uninstall` removes managed shims, the local
  `~/.local/bin/ctx-wire` binary, and only ctx-wire-owned hook entries, MCP
  server keys, and instruction blocks. It preserves unrelated hooks, MCP
  servers, rule-file content, gain logs, tee logs, and trust records.

### Filters and trust

Filters are declarative TOML, loaded in priority order: trusted project
`.ctx-wire/filters.toml`, then user `~/.config/ctx-wire/filters.toml`, then the
built-in set. A project filter file is ignored until you approve it with
`ctx-wire trust`. Bad filter files are skipped (fail-open) and never break a
command.

The built-in set covers build/test/install tools plus common agent inspection
commands. High-volume filters include `npm`, `pnpm`, `yarn`, `bun`, and `deno`
install/build/test commands, scoped `npm run build/lint/typecheck` style
scripts, `jest`, `vitest`, `tsc`, `eslint`, `pyright`, `pylint`, `flake8`,
`golangci-lint`, `cargo`, `go test/build/vet`, `pytest`, `ruff`, `mypy`,
`python -m unittest`, `rspec`, `phpunit`, `pip`, `docker`, `kubectl`, `gh`,
`brew`, `curl`, `wget`, `http`, `apt`, `dotnet test`, `mvn test`, and Gradle
test. Inspection filters include `rg`, `grep`, `git grep`, `ag`, `ack`, `ls`,
`eza`, `exa`, `lsd`, `find`, `tree`, `cat`, `sed`, `head`, `tail`, `nl`,
`git status`, `git diff`, `git show`, `git log`, `env`, and `printenv`.
Commands that can hide important detail, such as `cat` and `git diff`, are
capped in the agent-facing output and keep a scrubbed full-output spool when a
cap is hit.

Recovery spools normally live under the local data directory. If an agent
sandbox cannot write there, ctx-wire falls back to a per-user temp directory so
the `[full output: ...]` hint still works during dogfood.

### Configuration

An optional `~/.config/ctx-wire/config.toml` (honoring `XDG_CONFIG_HOME`, or
`CTX_WIRE_CONFIG` to override) tunes behavior. A missing file is fine; a
malformed one warns and is ignored.

```toml
[hooks]
# Command basenames the hook never rewrites and the runner never filters, for
# commands whose raw output the agent needs verbatim.
exclude_commands = ["curl", "playwright"]

# Wrapper prefixes peeled before routing: the inner command is rewritten and
# the prefix re-prepended, e.g. `docker exec web git status` becomes
# `docker exec web ctx-wire run git status`.
transparent_prefixes = ["docker exec web", "direnv exec ."]

[output]
# Extra compaction of filtered output (trim trailing whitespace, collapse
# blank-line runs) for a few more tokens.
ultra_compact = true

# Optional token budget framing for `ctx-wire gain --quota`.
# 0 means no budget; ctx-wire will show context-window multiples instead.
monthly_token_budget = 2000000
context_window = 200000

[update]
# ctx-wire self-updates in the background a few times a day, only on
# human-facing commands (gain, doctor) and never on the run/hook hot path. A
# newer release is downloaded, checksum-verified, and atomically installed by a
# detached process, so your command never blocks. On by default.
auto = true
# Minimum hours between background checks (default 2, i.e. ~12x/day). Set
# CTX_WIRE_NO_AUTOUPDATE=1 to disable for a single run or in CI.
interval_hours = 2
```

### Gain storage

`ctx-wire gain` reads the normal user log under the local data directory and a
sandbox fallback log under the user's temp directory. This lets transparent
agent hooks keep recording savings even when an agent sandbox can execute
commands but cannot append to `~/.local/share/ctx-wire/gain.jsonl`.

The report shows bytes saved, approximate tokens saved (using a simple
bytes-to-tokens estimate), savings by program, savings by invoking agent when
known, and a Token Opportunities table. Opportunities are commands or filter
paths that still emit a lot of bytes, which is the list to use when deciding
what semantic filter to improve next. Tiny opportunities under 1 KB are hidden
by default so the table stays actionable.

`ctx-wire gain --quota` shows month-to-date savings against a vendor-neutral
token budget or, when no budget is configured, as context-window multiples. It
also includes a per-agent split for attributed commands. Use
`ctx-wire gain --since 1h` or `ctx-wire gain clear` when checking whether a new
filter change improved fresh dogfood runs.

### Telemetry

Anonymous aggregate telemetry is enabled by default. It sends only counters used
for the public impact page:

- reported installs (successful `ctx-wire init <agent>` runs)
- total commands, raw bytes, emitted bytes, bytes saved, and estimated tokens
  saved
- per-program aggregate totals such as `cat`, `rg`, and `git`
- per-agent aggregate totals (claude, codex, ...), so a device using several
  agents reports a real split (token counts only, never dollars)

Impact counters are accumulated locally and flushed opportunistically after a
command finishes when at least one command is pending and either 5 minutes have
passed since the last attempt, 1000 commands are pending, or 10 MB has been
saved. `ctx-wire gain` also performs a best-effort full-summary flush, while
`ctx-wire gain --since ...` never reports telemetry. Network failures keep the
pending counters locally for a later retry and never fail the command.

The server derives country from Cloudflare's request metadata for aggregate
country stats. ctx-wire never sends commands, arguments, paths, raw output,
samples, repo names, usernames, hostnames, install IDs, or IP addresses.

Controls:

```sh
ctx-wire telemetry status
ctx-wire telemetry disable
ctx-wire telemetry enable
ctx-wire telemetry forget   # withdraw consent + erase local data (stays disabled)
```

`forget` erases the pending/last-reported counters and records a disabled
consent so it stays off: because telemetry is opt-out, it does not just delete
the config (that would re-enable it). Re-enable later with `telemetry enable`.

Set `CTX_WIRE_TELEMETRY=0` to disable telemetry for one process. Set
`CTX_WIRE_TELEMETRY_URL` to override the endpoint for tests or local Worker
development.

### Terminal color

Human-facing commands (`gain`, `doctor`, `explain`, `tune`, `discover`,
`learn`, `session`, `telemetry`, `verify`, `init`, `trust`) use color on
terminals and plain text when piped. Set `CTX_WIRE_COLOR=always` or
`CTX_WIRE_COLOR=never` to override detection. Set `CTX_WIRE_THEME=dark`,
`light`, `lite`, or `auto` to choose the palette; `auto` uses the terminal
background when it can be detected.

### Storage environment

`CTX_WIRE_GAIN=0` disables gain recording. `CTX_WIRE_GAIN_FILE` and
`CTX_WIRE_GAIN_FALLBACK_FILE` override the primary and fallback gain logs for
tests or sandboxed runs.

`CTX_WIRE_TELEMETRY=0` disables anonymous aggregate telemetry. `CTX_WIRE_TELEMETRY_URL`,
`CTX_WIRE_TELEMETRY_CONFIG`, and `CTX_WIRE_TELEMETRY_STATE` override telemetry
paths/endpoints for tests.

`CTX_WIRE_TEE=0` disables full-output spooling. `CTX_WIRE_TEE_DIR` and
`CTX_WIRE_TEE_FALLBACK_DIR` override the primary and fallback spool
directories. `ctx-wire doctor` reports which storage locations are writable.

## Daily dogfood

Install a local binary onto your `PATH` (agent hooks resolve `ctx-wire` from
there). Wiring an agent installs the binary too, so it is one step:

```sh
go build -o ctx-wire ./cmd/ctx-wire
./ctx-wire init claude    # or codex, cursor, gemini, ...
# or: just install        (builds with metadata, installs the binary only)
```

`ctx-wire init <agent>` copies the running binary to `~/.local/bin/ctx-wire`,
chmods it `755`, adds managed shims, wires that agent, and warns if `PATH`
resolves `ctx-wire` somewhere else. `just install` installs just the binary.

Wire up the agents you use:

```sh
ctx-wire init claude
ctx-wire init cursor
ctx-wire init codex          # then enable hooks + trust as it instructs
ctx-wire init gemini
ctx-wire init cline          # project .clinerules guidance
ctx-wire init windsurf       # project .windsurfrules guidance
ctx-wire init kilocode       # project .kilocode/rules guidance
ctx-wire init antigravity    # project .agents/rules guidance
ctx-wire init opencode       # global OpenCode plugin
ctx-wire init pi             # global Pi extension
ctx-wire init hermes         # global Hermes plugin
ctx-wire init copilot        # project .github guidance + hook config
ctx-wire init vscode         # writes .vscode/mcp.json (opt-in MCP tool)
ctx-wire init visualstudio   # writes ~/.mcp.json     (opt-in MCP tool)
```

Confirm the install is healthy:

```sh
ctx-wire version
ctx-wire verify     # filter conformance tests pass
ctx-wire doctor     # binary/PATH, hooks, MCP, storage, trust
```

Then run the safe daily loop: clear the window, work normally with your agent,
and review what happened. The review commands are read-only; only `gain clear`
mutates local gain history:

```sh
ctx-wire gain clear   # start a fresh measurement window
# ... work with your agent as usual ...
ctx-wire gain         # how many bytes/tokens were saved, by program
ctx-wire gain --quota # month-to-date savings vs budget/context windows, by agent
ctx-wire explain      # which commands are still burning tokens, and why
```

Use `ctx-wire explain <cmd>` on any specific command to see whether it is
wrapped and filtered, and `ctx-wire doctor` whenever something seems off.

## Troubleshooting

- **Hook not firing / commands not filtered.** Run `ctx-wire doctor`. If the
  `PATH` check warns that `ctx-wire` resolves to a different binary, reinstall
  (`ctx-wire init <agent>` or `just install`). If a hook shows "not installed", run
  `ctx-wire init <agent>` and restart the agent so it reloads its hook config.
- **Command is a pipeline, redirection, or shell builtin.** These pass through
  unchanged by design (see Known limitations). `ctx-wire explain '<cmd>'` shows
  the exact reason. Wrap the producer explicitly if you want it filtered, e.g.
  `ctx-wire run rg TODO . | head`.
- **Codex hook present but nothing happens.** Codex requires its hooks feature
  enabled and the hook trusted. `ctx-wire doctor` reports the feature flag;
  enable `[features] hooks = true` in `~/.codex/config.toml`, then run codex and
  trust the hook via `/hooks`. ctx-wire never bypasses Codex trust.
- **A rules-based agent still runs raw commands.** Cline, Windsurf, Kilo Code,
  and Antigravity use rules files, not terminal hooks. They improve default
  agent behavior, but the agent can still ignore the guidance. Use
  `ctx-wire explain <cmd>` and explicit `ctx-wire run ...` when you need
  certainty.
- **VS Code / Visual Studio Copilot is not filtering.** MCP is opt-in: the agent
  must choose ctx-wire's `run_command` or `read_file` tool. There is no
  transparent interception for these (see Known limitations).
- **`ctx-wire gain` is empty.** Nothing has been recorded yet in this window, or
  recording is disabled (`CTX_WIRE_GAIN=0`). Make sure commands are actually
  being routed through `ctx-wire run` (check with `ctx-wire doctor` and
  `ctx-wire explain <cmd>`), then retry after some agent activity.
- **Storage path not writable.** `ctx-wire doctor` reports gain/tee writability.
  ctx-wire falls back to a per-user temp directory when the primary data dir is
  unwritable (common in agent sandboxes), so capture keeps working; `gain`
  reads both locations. A `[fail]` line means neither the primary nor the
  fallback is writable.

## Known limitations

- **MCP and rules files are opt-in, not transparent.** MCP can only expose a
  callable tool; it cannot replace an editor's built-in terminal. So for VS Code
  and Visual Studio Copilot, the agent must choose ctx-wire's `run_command` or
  `read_file` tool (steered by the tool descriptions). Cline, Windsurf, Kilo
  Code, and Antigravity are prompt/rules guidance only. OpenCode, Pi, and Hermes
  depend on their plugin surfaces. The hook-based agents (Claude Code, Cursor,
  Codex, Gemini CLI) get transparent interception.
- **Pipelines wrap only the final stage; redirections are passthrough.** For a
  pipeline, ctx-wire wraps just the last stage (e.g. `rg TODO . | wc -l` becomes
  `rg TODO . | ctx-wire run wc -l`), so the producers run raw and computing
  consumers like `wc`/`grep`/`jq` still see the true stream while the
  agent-facing final output is filtered. If the last stage is not wrappable (a
  redirect, builtin, or subshell), the whole pipeline passes through unchanged. A
  segment with a top-level `>`/`<` redirect is left unwrapped, because wrapping it
  would route ctx-wire's output into the redirect target and change what you
  capture. Subshells `(...)`, brace groups `{ ...; }`, and shell builtins/keywords
  are passed through for the same safety reason. The rewriter is a conservative,
  POSIX-ish recognizer, not a full shell parser; `ctx-wire explain` reports the
  exact decision.
- **JSON payloads are not reduced.** This is enforced by content, not just by
  command: if a filter would truncate a complete, valid JSON document on stdout,
  ctx-wire emits the document whole instead (up to ~1 MiB; a larger one is
  replaced with a notice rather than cut mid-structure), because capping or
  truncating JSON produces invalid output that breaks downstream parsers. So a
  single-line JSON flowing through `cat`, a helper script, or a statusline stays
  intact. Commands whose job is to compact JSON (`jq`) opt out with
  `reduce_json = true` and keep capping. Known JSON commands (`go list -json`,
  `terraform show -json`, the `tofu` equivalents) also have explicit passthrough
  filters; their non-JSON variants are still compacted.
- **Project filters require trust.** A project-local `.ctx-wire/filters.toml` is
  ignored until you approve it with `ctx-wire trust` (recorded by SHA-256). If
  the file changes after approval, it reverts to untrusted until re-approved.
  Revoke approval at any time with `ctx-wire untrust`. Bad filter files are
  skipped (fail-open) and never break a command.

## Backed by

[LogicStar AI](https://logicstar.ai) · [SashiDo.io](https://www.sashido.io)
