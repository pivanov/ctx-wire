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

# File-tools capture experiment (Claude only, default off): the PreToolUse
# hook denies built-in Read/Grep calls that map EXACTLY to a shell command,
# suggesting the filtered equivalent (large unranged reads -> `nl -ba`, Grep ->
# the matching `rg` form). Anything uncertain passes through untouched, a
# denied request retried within 60s is allowed (loop-breaker), and a deny is
# only ever issued after it is recorded. Toggle with
# `ctx-wire init claude --capture-files | --no-capture-files`.
capture_file_tools = false

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
# marked explicitly, and the full scrubbed output is spooled to disk (the
# `[full output: ...]` hint names the file). Deterministic head+tail only,
# never a generated summary. "none" disables the ceiling entirely.
truncate = "default"

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
