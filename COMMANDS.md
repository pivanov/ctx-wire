# ctx-wire commands

The complete command list, plus deep dives on the diagnostic and integration
commands. For the config file and environment variables see
[CONFIGURATION.md](CONFIGURATION.md); for problems and known limitations see
[TROUBLESHOOTING.md](TROUBLESHOOTING.md).

## All commands

| Command | What it does |
|---|---|
| `ctx-wire run <cmd> [args]` | Execute a command and filter/scrub its output |
| `ctx-wire fetch <hash>` | Recover the full scrubbed output spooled for a truncated or failed command (the `[full output: ctx-wire fetch <hash>]` handle) |
| `ctx-wire mcp` | Serve `run_command` and `read_file` filtering tools over MCP (stdio) |
| `ctx-wire mcp-wrap [--compress] -- <server>` | Transparently relay a stdio MCP server and measure per-tool token cost; `--compress` also reduces verbose accessibility snapshots (chrome-devtools, Playwright), spooling the raw result locally |
| `ctx-wire mcp-wrap install [--compress] \| uninstall <server>` | Wrap (or revert) a server in your MCP config so its tool output is measured (and with `--compress`, reduced); backs up the config, needs an agent restart |
| `ctx-wire hook <agent>` | Run as an agent pre-tool hook (reads JSON on stdin) |
| `ctx-wire rewrite <line>` | Print the rewritten form of a shell command line |
| `ctx-wire init <agent> [--no-mcp]` | Install the binary into `~/.local/bin` and wire an agent (claude, cursor, codex, gemini, cline, windsurf, kilocode, antigravity, opencode, pi, hermes, copilot, vscode, visualstudio). Adds PATH shims only for steering-only agents; hook/plugin-capable agents are covered by their hook/plugin. `init claude` also relays known snapshot-heavy MCP servers (chrome-devtools, Playwright) through `mcp-wrap --compress`, printing each change; skip with `--no-mcp`, revert per server with `mcp-wrap uninstall`, and `ctx-wire uninstall` reverts all wraps. `--capture-files` / `--no-capture-files` toggle the opt-in file-tools capture experiment (see CONFIGURATION.md) |
| `ctx-wire shims [status\|install\|uninstall]` | Manage the optional default PATH shims: inspect them, opt in on a hook/plugin-capable agent, or remove them if they slow shell startup. `uninstall` removes only ctx-wire-managed shim files |
| `ctx-wire update [--check]` | Upgrade to the latest release (checksum-verified, atomic, with rollback); `--check` only reports |
| `ctx-wire uninstall [<agent>]` | With no argument, removes all ctx-wire wiring (binary, managed shims, every agent's hook/config entries). With an `<agent>` (claude, codex, cursor, ...), removes only that agent's hooks/instructions and leaves the binary, shims, and other agents intact |
| `ctx-wire trust` | Approve this project's `.ctx-wire/filters.toml` by hash |
| `ctx-wire untrust` | Revoke trust for this project's `.ctx-wire/filters.toml` |
| `ctx-wire gain` | Report token savings recorded so far, with per-program, per-source, and per-agent breakdowns |
| `ctx-wire gain --since 1h` | Report only recent savings |
| `ctx-wire gain --history [--top N] [--agent <name>]` | Recent commands (time, invoking agent, savings, full command), newest last |
| `ctx-wire gain --agent <name>` | Keep only one invoking agent's commands; composes with any view (e.g. `--history`, `--daily`) |
| `ctx-wire gain --daily \| --weekly \| --monthly` | Savings grouped by period |
| `ctx-wire gain --graph` | ASCII bar graph of daily saved bytes |
| `ctx-wire gain --json \| --csv` | Export the summary/daily breakdown |
| `ctx-wire gain --quota [--budget <tokens>] [--window <tokens>]` | Month-to-date savings vs a vendor-neutral token budget, with a per-agent split |
| `ctx-wire gain clear` | Clear local gain history for a fresh dogfood window |
| `ctx-wire explain <cmd>` | Diagnose how ctx-wire handles one command (filter, mode, hook) |
| `ctx-wire inspect [n] \| --list` | Show raw-vs-filtered for a recent command, so you can audit what was removed (needs `[retention]`) |
| `ctx-wire tune [--since 24h] [--top N]` | Higher-level filter improvement report from gain data (read-only) |
| `ctx-wire discover [--since 24h] [--top N] [--all]` | Find agent commands (Claude/Codex transcripts) that escaped ctx-wire (read-only) |
| `ctx-wire learn [--since D] [--all] [--min N] [--write]` | Mine Claude transcripts for failed->corrected commands; `--write` saves `.claude/rules/cli-corrections.md` |
| `ctx-wire session [--since 24h] [--top N] [--all]` | Per-session ctx-wire adoption across agent transcripts (read-only). Claude sessions also show built-in Read/Grep tool counts and read-before-edit refusals, the traffic that bypasses ctx-wire entirely |
| `ctx-wire tune preview` | Dry-run the sanitized bundle contents without writing files |
| `ctx-wire tune bundle [--out PATH]` | Write a sanitized tune bundle archive for manual sharing |
| `ctx-wire tune issue [--open]` | Print or open a sanitized GitHub issue draft |
| `ctx-wire tune draft <program>` | Scaffold a starter filter for a program from a real captured transcript sample (`--preview`/`--write`) |
| `ctx-wire filters pull <name> \| publish <name>` | Share filters: pull a community filter (parsed and inline-tested, installed untrusted) or package a local one |
| `ctx-wire telemetry [status\|preview\|enable\|disable\|improvements\|forget]` | Show or change opt-out anonymous aggregate telemetry (on by default); `improvements [on\|off]` toggles only the per-command breakdown (stats stay on), `preview` prints the exact payload, and `forget` withdraws consent and erases local data |
| `ctx-wire doctor [--all] [--recent N] [--verbose]` | Check install/hooks/MCP/storage/trust health (read-only). Optional `[off]` checks (integrations not set up) are hidden behind a one-line count; `--all` shows them. `--recent N` lists the N most recent recorded commands; `--verbose` implies `--recent 5` |
| `ctx-wire verify [filter]` | Run the built-in filter conformance tests |
| `ctx-wire version` | Print version and build metadata |

## Command details

The diagnostic and integration commands, in more depth.

## `ctx-wire explain`

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

## `ctx-wire tune`

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

## `ctx-wire doctor`

`doctor` is read-only. By default it shows only actionable state (`ok`, `warn`,
`fail`); optional integrations that simply are not set up (`off`) are collapsed
into a one-line count, and `--all` shows them. It reports the running binary and whether `ctx-wire` on
your `PATH` is that same binary, whether managed command shims are installed and
first on `PATH`, whether any shim captures have been recorded, which agent
hooks/rules are installed (Claude, Cursor, Codex, Gemini, Cline, Windsurf,
Kilo Code, Antigravity, OpenCode, Pi, Hermes, Copilot, plus Codex's
hooks-feature flag), whether MCP config exists for VS Code / Visual Studio,
which Claude MCP servers are relayed through `mcp-wrap` (warning when a wrap
points at a stale ctx-wire path that would break the server if it disappears),
whether the gain and tee directories are writable (with the sandbox fallback),
and the project filter trust state. It prints counts only by default; pass
`--recent N` (or `--verbose`) to also list the last N scrubbed commands. Exit
code is `0` when healthy or only warnings, `1` for a broken install (unwritable
storage, unloadable filter registry). When managed shims actually resolve first
on `PATH` while a hook/plugin already covers those commands, `doctor` flags the
startup cost and points at `ctx-wire shims uninstall`.

## `ctx-wire shims`

Manage the optional default PATH shims. `init` installs them only for
steering-only agents; on a hook/plugin-capable agent the hook/plugin already
covers model-visible commands, so shims add no coverage and, when the shim dir
is early on `PATH`, slow every shell prompt.

- `ctx-wire shims status` reports, across **every** managed shim dir on `PATH`
  (not just the install dir), how many shims are installed and how many actually
  resolve first on `PATH` (the ground truth for whether they are on the hot path).
- `ctx-wire shims install` installs the default shim set and records that you
  want them, so the advisory never nudges you to remove them.
- `ctx-wire shims uninstall` removes only ctx-wire-managed shim files, across all
  managed dirs, leaving the binary, hooks, config, filters, and gain/tee data
  intact.

After an upgrade, an existing hook/plugin-only install is **not** auto-modified:
`gain`/`doctor` print a one-time advisory that those shims can be removed with
`ctx-wire shims uninstall`. Set `CTX_WIRE_KEEP_SHIMS=1` to silence it.

## Integrations (`ctx-wire init <target>`)

- **self**: copies the current binary to `~/.local/bin/ctx-wire` with executable
  permissions. For steering-only agents (or on an explicit `ctx-wire shims
  install`) it also installs managed command shims next to it. The shims cover
  common agent commands such as `git`, `rg`, `grep`, `cat`, `sed`, `head`,
  `tail`, `sort`, `bun`, `npm`, `go`, and `cargo`. Existing non-ctx-wire files
  are never overwritten, and ctx-wire only creates a shim when the real command
  is already installed elsewhere on `PATH` (so it never makes `jq` or `git`
  appear to exist). Normal terminal commands bypass ctx-wire and exec the real
  tool directly, preserving native colors/progress behavior.
- **Claude Code, Cursor, Codex, Gemini CLI, Cline, Windsurf, Kilo Code,
  Antigravity, OpenCode, Pi, Hermes, Copilot, VS Code, Visual Studio**: each
  agent init installs the local binary. Managed command shims are added only for
  **steering-only** agents (Cline, Windsurf, Kilo Code, Antigravity, VS Code,
  Visual Studio), whose shim is their only coverage; hook/plugin-capable agents
  (Claude, Codex, Cursor, Gemini, Copilot, OpenCode, Pi, Hermes) are covered by
  their hook/plugin, so `init` no longer adds shims for them (they would only
  add shell-prompt latency). Manage shims explicitly with `ctx-wire shims
  install | uninstall | status`. Each shim
  removes its own directory from `PATH`, resolves the real command, and then
  calls `ctx-wire run` with the real absolute path only when the process is
  agent-marked (`CTX_WIRE_AGENT_SHIMS=1` / `CTX_WIRE_SHIMS=1`) or launched from
  a **steering-only** agent (Cline, Windsurf, Kilo Code, Antigravity, VS Code,
  Visual Studio). Under an agent that already rewrites commands itself, a hook
  (Claude, Codex, Cursor, Gemini, Copilot) or a plugin (OpenCode, Pi, Hermes),
  the shim passes through instead of wiring: that agent's own rewrite covers the
  model-visible commands, so a second shim layer would only re-wrap shell
  plumbing and corrupt command substitutions like `result=$(cat file)`. Set
  `CTX_WIRE_SHIMS=1` to force the shim to wire even under those agents. That
  avoids recursive shim calls and keeps your ordinary terminal path native. Shim captures are counted in a scrubbed
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
  For Codex, `init` also sets `CTX_WIRE_AGENT = "codex"` in
  `shell_environment_policy.set` in `~/.codex/config.toml`, so `gain` attributes
  direct `ctx-wire run` commands even when the Codex sandbox blocks the
  `ps`-based agent detection. That key only labels ctx-wire telemetry (it is not
  a hooks or trust change), a user-modified value is never overwritten, and
  `ctx-wire uninstall` removes exactly that key.
  The Claude hook also respects your Bash permission rules: if a command matches
  a `permissions.deny` or `permissions.ask` rule in your `settings.json`,
  ctx-wire does **not** auto-approve the rewritten form. It steps aside so Claude
  applies its own decision to the original command, instead of the wrapper hiding
  the command from your deny/ask rules. Commands with no matching rule keep the
  transparent allow-and-filter behavior.
  For Claude, `init`, `uninstall`, and `doctor` act on **every** config directory,
  not just the default `~/.claude`: whatever `CLAUDE_CONFIG_DIR` points at, plus
  any `~/.claude*` sibling that has both a `settings.json` file and a `projects/`
  directory. So a machine running several Claude configs (`~/.claude`,
  `~/.claude-main`, ...) gets all of them wired from one `init claude`, reverted
  together by `uninstall`, and `doctor` warns when a real config is left unhooked.
  On **Codex**, ctx-wire is a filter, not a permission gate: by default it
  auto-approves the commands it wraps, so Codex runs uninterrupted and safety
  stays with Codex's own approval policy. Commands ctx-wire did not wrap are
  never touched, so Codex decides those normally. Set `CTX_WIRE_CODEX_SAFE=1`
  to restore an audited gate instead, only read/build/test commands that hide
  nothing auto-approve, and everything else falls through to Codex's own prompt.
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
  server keys, instruction blocks, and the Codex agent-attribution env key
  (skipped if you changed its value). It then purges ctx-wire's own config and
  data directories wholesale, so filters, trust records, gain logs, tee
  captures, and telemetry config/state are all removed too. Unrelated hooks, MCP
  servers, and rule-file content are left intact.

## Filters and trust

Filters are declarative TOML, loaded from three sources in precedence order:
trusted project `.ctx-wire/filters.toml`, then user
`~/.config/ctx-wire/filters.toml`, then the built-in set. When several filters
match one command, the most specific (longest matched span) wins; precedence only
breaks ties (see [FILTERS.md](FILTERS.md#selection)). A project filter file is
ignored until you approve it with `ctx-wire trust`. Bad filter files are skipped
(fail-open) and never break a command.

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

When a cap or failure spools one, the agent-facing output ends with a
`[full output: ctx-wire fetch <hash>]` hint; running `ctx-wire fetch <hash>`
streams the full scrubbed copy back. The hash addresses the spool by the
sha256 of its scrubbed contents, so the handle is stable.

Recovery spools normally live under the local data directory. If an agent
sandbox cannot write there, ctx-wire falls back to a per-user temp directory so
the hint still works during dogfood.
