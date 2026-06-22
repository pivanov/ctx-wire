# ctx-wire configuration

The optional config file, gain storage, telemetry, terminal color, and storage
environment variables. For commands see [COMMANDS.md](COMMANDS.md); for problems
and known limitations see [TROUBLESHOOTING.md](TROUBLESHOOTING.md).

## Configuration

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

# Native-Read ceiling (Claude only, ON by default, wired automatically by
# `ctx-wire init claude`): a PostToolUse hook reshapes LARGE UNRANGED built-in
# Read output to its head + tail with a recoverable `ctx-wire fetch <hash>`
# handle, so the middle is elided from context but kept on disk. Emitted bytes
# are secret-scrubbed (the native Read tool bypasses scrubbing, so this is a net
# gain). A ranged Read (offset/limit) is the agent's own bound and is never
# reshaped. Savings record in `ctx-wire gain` under a "Read" program. Values:
# "on" (default), "measure" (log the would-be reclaim without rewriting), "off"
# (opt this machine out). Env CTX_WIRE_READ_CEILING overrides per run.
read_ceiling = "on"

# Files that must reach the agent whole: a `cat`/`nl` read of a file whose
# basename matches one of these globs skips output capping (the per-filter line
# cap and the passthrough ceiling), so an instruction or skill file is never
# truncated. Output is still secret-scrubbed. These EXTEND the built-in defaults
# (SKILL.md, AGENTS.md, CLAUDE.md), they do not replace them.
full_files = ["*.skill", "PLAYBOOK.md"]

[output]
# Extra compaction of filtered output (trim trailing whitespace, collapse
# blank-line runs) for a few more tokens.
ultra_compact = true

# Truncation dial: scales every filter's numeric caps (truncate_lines_at,
# head/tail, max_lines, group caps) without editing TOML. "light" doubles the
# caps (keep more), "aggressive" halves them (save more, floor 1), "none"
# removes them, "default" applies them as written. Filters still only act on
# output they positively recognize; the dial changes how much of it is kept,
# never what gets filtered. Override per invocation with CTX_WIRE_TRUNCATE.
#
# The dial also scales the passthrough ceiling: output with no matching filter
# streams unmodified up to a generous size cap (~64 KB at "default"); beyond
# it the head and the tail of each stream are kept, the omitted middle is
# marked explicitly, and the full scrubbed output is spooled to disk; recover it
# with `ctx-wire fetch <hash>` (printed as the `[full output: ...]` hint).
# Deterministic head+tail only,
# never a generated summary. "none" disables the ceiling entirely.
truncate = "default"

# Collapse runs of third-party / language-runtime stack frames (node_modules,
# site-packages, JDK runtime packages, ...) into a "... (+N library frames
# hidden)" marker, keeping the exception header, every application frame, and
# "caused by" links. Off by default: a stack trace is often the answer, so only
# frames whose source path is provably a library are hidden, and the full raw
# trace is still spooled to disk. Recognizes Python, Node.js, and Java/JVM
# traces. Applies to a matched command's filtered stdout and stderr; commands
# ctx-wire streams or bypasses (dev servers, interactive tools) are untouched.
# Override per invocation with CTX_WIRE_STRIP_STACKTRACES=1 (or =0).
strip_stacktraces = false

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

[retention]
# Recent-outputs store behind `ctx-wire inspect` (a raw-vs-filtered audit
# trail). A deliberate exception to "do not persist successful output", so it is
# OFF unless enabled here. CTX_WIRE_RETENTION=0 force-disables it for one run.
enabled = false
# Also keep the scrubbed raw (pre-filter) body, which `inspect` needs for a full
# raw-vs-filtered audit. The larger persistence cost, off by default;
# CTX_WIRE_RETENTION_RAW=0 drops just this tier for one run.
raw_bodies = false
# Cap on how many recent entries are kept (0 uses the built-in default).
max_entries = 0

[dedup]
# When a read-only command re-runs with byte-identical output, emit a short
# recoverable reference instead of the body (the command still runs; only the
# re-emission is saved). ON by default; it implies the retention store above is
# recording, so the reference can be recovered via `inspect`. Opt a machine out
# by uncommenting `enabled = false`.
# enabled = false
# How recent the prior run must be to dedup against it (default 60), so a
# reference is only emitted while the unchanged body is likely still in context.
# Disable dedup for a single run with CTX_WIRE_NO_DEDUP=1 or `ctx-wire run
# --no-dedup ...` (emit the full body instead of a reference).
recency_minutes = 60
```

## Gain storage

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

## Telemetry

Anonymous aggregate telemetry is opt-out: it is on by default and stays on until
you disable it. The first interactive `ctx-wire gain` for an undecided user shows
a one-time notice with a fixed example payload and how to turn it off. An explicit
disable (or `forget`) is recorded and never reversed by an update; only an
undecided (never-chosen) state defaults to on. To inspect your exact local
payload, run `ctx-wire telemetry preview`.

When enabled, telemetry sends only counters used for the public impact page:

- reported installs (successful `ctx-wire init <agent>` runs)
- total commands, raw bytes, emitted bytes, bytes saved, and estimated tokens
  saved
- per-program aggregate totals such as `cat`, `rg`, and `git`
- per-agent aggregate totals (claude, codex, ...), so a device using several
  agents reports a real split (token counts only, never dollars)

Impact counters are accumulated locally and flushed opportunistically after a
command finishes: the first flush waits for a meaningful batch (1000 pending
commands or 10 MB saved), and after that flushes are rate-limited to at most once
every 30 minutes. `ctx-wire gain` also performs a best-effort full-summary flush,
while `ctx-wire gain --since ...` never reports telemetry. Network failures keep
the pending counters locally for a later retry and never fail the command.

The server derives country from Cloudflare's request metadata for aggregate
country stats. ctx-wire never sends commands, arguments, paths, raw output,
samples, repo names, usernames, hostnames, install IDs, or IP addresses.

Controls:

```sh
ctx-wire telemetry status
ctx-wire telemetry preview
ctx-wire telemetry enable
ctx-wire telemetry disable             # turn ALL telemetry off
ctx-wire telemetry improvements off    # keep stats; drop only the per-command detail
ctx-wire telemetry forget   # withdraw consent + erase local data (stays disabled)
```

`forget` erases the pending/last-reported counters and records a disabled
consent so it stays off and the notice does not reappear. Re-enable later with
`telemetry enable`.

Set `CTX_WIRE_TELEMETRY=0` to disable all telemetry for one process, or
`CTX_WIRE_TELEMETRY_IMPROVEMENTS=0` to drop only the per-command breakdown while
the aggregate stats keep flowing. Set `CTX_WIRE_TELEMETRY_URL` to override the
endpoint for tests or local Worker development.

## Codex hook permissions

The Codex hook is a filter, not a permission boundary: by default ctx-wire
auto-approves the commands it wraps so the agent runs without extra prompts, and
safety stays with Codex's own approval policy. Set `CTX_WIRE_CODEX_SAFE=1` (in
Codex's shell env) to restore an audited gate, only read/build/test commands
that hide nothing or redirect nowhere auto-approve, and everything else falls
through to Codex's own prompt. See COMMANDS.md for the full behavior.

## Terminal color

Human-facing commands (`gain`, `doctor`, `explain`, `tune`, `discover`,
`learn`, `session`, `telemetry`, `verify`, `init`, `trust`) use color on
terminals and plain text when piped. Set `CTX_WIRE_COLOR=always` or
`CTX_WIRE_COLOR=never` to override detection. Set `CTX_WIRE_THEME=dark`,
`light`, `lite`, or `auto` to choose the palette; `auto` uses the terminal
background when it can be detected.

## Storage environment

`CTX_WIRE_GAIN=0` disables gain recording. `CTX_WIRE_GAIN_FILE` and
`CTX_WIRE_GAIN_FALLBACK_FILE` override the primary and fallback gain logs for
tests or sandboxed runs.

`CTX_WIRE_TELEMETRY=0` disables anonymous aggregate telemetry. `CTX_WIRE_TELEMETRY_URL`,
`CTX_WIRE_TELEMETRY_CONFIG`, and `CTX_WIRE_TELEMETRY_STATE` override telemetry
paths/endpoints for tests.

`CTX_WIRE_TEE=0` disables full-output spooling. `CTX_WIRE_TEE_DIR` and
`CTX_WIRE_TEE_FALLBACK_DIR` override the primary and fallback spool
directories. `ctx-wire doctor` reports which storage locations are writable.

`CTX_WIRE_RETENTION=0` turns off the recent-outputs store (the `[retention]`
store behind `ctx-wire inspect`) for a single run; `CTX_WIRE_RETENTION_RAW=0`
drops just the raw (pre-filter) tier, an escape hatch for a sensitive command.

`CTX_WIRE_NO_DEDUP=1` disables repeat-command dedup for one run (same as
`ctx-wire run --no-dedup`). `CTX_WIRE_KEEP_SHIMS=1` suppresses the advisory that
suggests removing PATH shims a hook/plugin has made redundant, for users who
deliberately keep their shims; it does not change which shims are installed.
