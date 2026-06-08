# Writing ctx-wire filters

A filter is a small declarative rule that compacts one program's output before an
AI agent reads it. This is the authoring reference: the TOML schema, the order
the stages run in, the inline test block, and the flow from a captured sample to
a published filter.

For the command list see [COMMANDS.md](COMMANDS.md); for where filter files load
from and how trust works see [COMMANDS.md](COMMANDS.md#filters-and-trust).

## Where filters live

Filters come from three sources, loaded in this precedence order (it only breaks
ties; see [Selection](#selection) for how the actual filter is picked):

1. Trusted project file: `.ctx-wire/filters.toml` (loads only after `ctx-wire trust`).
2. User file: `~/.config/ctx-wire/filters.toml`.
3. The built-in set shipped in the binary.

A project file is ignored until you approve it by hash with `ctx-wire trust`, and
a bad file is skipped (fail-open) so it never breaks a command. You can author
and test a filter without trusting it: `ctx-wire verify --file <path>` runs the
file's own inline tests without loading or applying it.

## File structure

```toml
schema_version = 1

[filters.<name>]
# matching + transform fields (see below)

[[tests.<name>]]
# inline conformance tests for filters.<name> (see Testing)
```

`schema_version` must be `1`. `<name>` is the filter id; the `[[tests.<name>]]`
blocks attach to the filter of the same name. One file may define many filters
and many tests per filter.

## Matching

A filter only runs when its command pattern matches.

| Field | Type | Purpose |
| --- | --- | --- |
| `match_command` | regex (string) | Required. Matched against the full command line. The filter runs only on a match. |
| `priority` | int | Tie-breaker only: applied when two filters match spans of the **same length** (see Selection). Default `0`. |
| `description` | string | Human note stored with the filter. No effect on output. |

`match_command` is a Go regexp (RE2). Anchor it (`^go\s+(test|build|vet)\b`) so a
broad word does not match unrelated commands.

### Selection

When several filters match one command, ctx-wire does **not** pick the first or
the highest-priority one. It picks the **most specific**: the filter whose
`match_command` covers the longest span of the command line. So a precise pattern
(`^gradle .*bootRun`) beats a broad one (`^gradle`) on `gradle bootRun`, without
either needing a `priority`.

`priority` is only consulted to break a tie between two filters whose matched
spans are exactly the same length; after that, source order decides (project
before user before built-in). The practical rule: **to beat a broad filter, write
a longer-matching `match_command`, not a higher `priority`.** Raising `priority`
does nothing against a filter that already matches a longer span.

## Transform stages

Stages run in the fixed order below. Every field is optional; omit a field to
skip its stage. Pointer-typed fields (`*int`, `*string`) are "unset" when absent,
which is different from `0` or `""`.

| Order | Field | Type | What it does |
| --- | --- | --- | --- |
| 1 | `strip_ansi` | bool | Remove ANSI color/escape codes before anything else. |
| 2 | `replace` | array of `{pattern, replacement}` | Line-by-line regex substitution, rules applied in sequence. `replacement` may use `$1` capture refs. |
| 3 | `match_output` | array of `{pattern, message, unless?}` | If `pattern` matches the whole (post-replace) blob, replace all output with `message`. First rule wins. A rule is skipped if its optional `unless` regex also matches. This is the "collapse a clean run to one line" stage. |
| 4 | `strip_lines_matching` / `keep_lines_matching` | array of regex | Drop lines that match (strip), or keep only lines that match (keep). Use one or the other, not both. |
| 5 | `truncate_lines_at` | int | Cap each line to N runes; longer lines are cut and the result is marked truncated. |
| 6 | `head_lines` / `tail_lines` | int | Keep the first N (`head`), the last N (`tail`), or both ends with an `... (N lines omitted)` marker in the middle. |
| 6b | `group_by` | table | Bucket lines by a regex key, then cap per bucket and total buckets (see below). Runs after head/tail, before `max_lines`. |
| 7 | `max_lines` | int | Absolute cap on total lines (the omit marker counts). On a non-zero exit ctx-wire keeps the tail instead of the head, because the error is usually at the end. |
| 8 | `on_empty` | string | If the output is empty after all stages, emit this string instead. The "nothing left means it succeeded" summary. |

`filter_stderr` (bool) additionally routes the command's stderr through the same
pipeline; by default only stdout is filtered.

### `group_by`

```toml
[filters.<name>.group_by]
key           = "regex with one capture group"  # capture 1 is the bucket key
max_per_group = 3                                 # lines kept per bucket (>0)
max_groups    = 10                                # buckets kept total (>0)
omit_label    = "... %d more in %s"               # fmt template: (omittedCount, groupKey)
```

Use it for output that is a long list of items sharing keys (e.g. lint findings
grouped by file): keep a few lines per key and a count of the rest.

## Two safety rules to know

These are enforced by the runner regardless of your filter, so you do not have to
encode them:

- **Complete JSON is never truncated.** If a stage would cut a valid JSON
  document, ctx-wire emits it whole instead, so downstream parsers stay happy. A
  filter whose job is to compact JSON opts out with `reduce_json = true` and keeps
  capping.
- **Failures keep their evidence.** On a non-zero exit the runner suppresses your
  synthetic-success summaries (`match_output` and `on_empty`) and keeps the tail
  on truncation, so an error is never replaced by "ok". Author your tests for that
  path with `failed = true` (see below).

| Field | Type | Purpose |
| --- | --- | --- |
| `reduce_json` | bool | Opt this filter out of the keep-JSON-intact rule (for `jq`-style tools that exist to shrink JSON). |

## Testing

Every filter should carry inline tests. They are conformance tests, run by
`ctx-wire verify`, and are trust-free: they exercise the filter's own rules
against fixed input, never a real command.

```toml
[[tests.<name>]]
name     = "clean run collapses to one line"
input    = "ok  ./pkg  0.012s\nok  ./pkg2  0.004s\n"
expected = "go: ok"
```

| Field | Type | Purpose |
| --- | --- | --- |
| `name` | string | Label shown in `verify` output. |
| `input` | string | Raw text fed to the filter (use `\n` for newlines, or a TOML multiline string). |
| `expected` | string | The output the filter must produce. Compared after trimming trailing newlines. |
| `draft` | bool | Marks a scaffolded, not-yet-finished case. A draft test fails `verify` until you finish it and remove the marker (`tune draft` writes these). Built-in filters may never ship one; a local file can keep one with `verify --allow-draft`. |
| `failed` | bool | Runs the case the way the runner treats a non-zero exit: suppress `match_output`/`on_empty` summaries, keep the tail on truncation. Use it to assert that a failing run still shows its error instead of collapsing to "ok". |

Run them:

```bash
ctx-wire verify --file ./my-filters.toml        # a standalone file
ctx-wire verify --project                       # this project's .ctx-wire/filters.toml
ctx-wire verify <name>                           # one built-in filter by name
```

A `failed = true` regression is the cheapest insurance against the worst filter
bug, silently faking success on a failed run. The flag only changes the result
when a summary (`match_output` or `on_empty`) would otherwise fire, so a
load-bearing case feeds input that collapses to a summary unless suppression
kicks in. The classic one is a failed run whose output is all noise: after
stripping, nothing is left, and `on_empty` would falsely report "ok".

```toml
[[tests.golangci-lint]]
name     = "no-findings failure is not faked as ok"
input    = ""
expected = ""
failed   = true
```

Without `failed = true` this case produces `golangci-lint: ok` (from `on_empty`)
and the test fails, which is the proof the flag is doing real work. A test whose
input still has a leftover line after stripping would pass with or without the
flag, so it asserts the rules, not the failure path.

## A complete example

```toml
schema_version = 1

[filters.mytool]
description   = "Compact mytool build output"
match_command = "^mytool\\s+(build|check)\\b"
strip_ansi    = true
match_output  = [
  { pattern = "(?m)^Build succeeded", message = "mytool: ok", unless = "(?m)(error|warning):" },
]
strip_lines_matching = ["^\\s*$", "^Compiling "]
max_lines = 120
on_empty  = "mytool: ok"

[[tests.mytool]]
name     = "clean build collapses"
input    = "Compiling a\nCompiling b\nBuild succeeded in 1.2s\n"
expected = "mytool: ok"

[[tests.mytool]]
name     = "error survives the rules"
input    = "Compiling a\nerror: missing semicolon at line 4\n"
expected = "error: missing semicolon at line 4"

# Load-bearing failure test: a failed build whose output is all noise strips to
# empty, and on_empty would fake "mytool: ok". failed = true suppresses on_empty
# so the empty (truthful) result stands. Drop the flag and this test fails.
[[tests.mytool]]
name     = "noise-only failed build is not faked as ok"
input    = "Compiling a\nCompiling b\n"
expected = ""
failed   = true
```

## Authoring flow

1. **Scaffold from a real sample.** `ctx-wire tune draft <program>` builds a
   starter filter (with draft tests) from a captured transcript of that program.
   Preview with `--preview`, write it with `--write`.
2. **Refine and test.** Edit the rules, fill in the draft tests' `expected`, drop
   the `draft` marker, and run `ctx-wire verify --file <path>` until green. Add a
   `failed = true` case so the failure path is covered.
3. **Use it locally.** Put it in `.ctx-wire/filters.toml` and run `ctx-wire trust`,
   or in `~/.config/ctx-wire/filters.toml` for all your projects.
4. **Share it.** `ctx-wire filters publish <name>` packages a local filter (with
   its tests) for sharing; `ctx-wire filters pull <name>` installs a community
   filter, which is parsed and inline-tested before it lands, and stays untrusted
   until you approve it.
