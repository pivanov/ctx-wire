# ctx-wire troubleshooting & limitations

Common problems and the things ctx-wire deliberately does not do. For the command
list see [README.md](README.md#commands) and [COMMANDS.md](COMMANDS.md); for
config and environment variables see [CONFIGURATION.md](CONFIGURATION.md).

## Troubleshooting

- **Hook not firing / commands not filtered.** Run `ctx-wire doctor`. If the
  `PATH` check warns that `ctx-wire` resolves to a different binary, reinstall
  (`ctx-wire init <agent>` or `just install`). If a hook shows "not installed", run
  `ctx-wire init <agent>` and restart the agent so it reloads its hook config.
- **Commands fail or the agent errors after an upgrade (`EAGAIN` /
  `posix_spawn`, a process storm, or "command not found").** An upgrade replaces
  the ctx-wire binary but not the PATH shims, so a duplicate or stale install can
  leave old shims on PATH that misbehave. ctx-wire self-heals this: it
  regenerates managed shims to the current template on the next human-facing
  command (`doctor`, `gain`, `version`, `update`). `ctx-wire doctor` also reports
  duplicate ctx-wire binaries or shim dirs on PATH, and `ctx-wire init <agent>`
  regenerates the shims immediately.
- **Command is a pipeline, redirection, or shell builtin.** These pass through
  unchanged by design (see Known limitations). `ctx-wire explain '<cmd>'` shows
  the exact reason. Wrap the producer explicitly if you want it filtered, e.g.
  `ctx-wire run rg TODO . | head`.
- **Codex hook present but nothing happens.** Codex requires its hooks feature
  enabled and the hook trusted. `ctx-wire doctor` reports the feature flag;
  enable `[features] hooks = true` in `~/.codex/config.toml`, then run codex and
  trust the hook via `/hooks`. ctx-wire never bypasses Codex trust.
- **`gain` shows `(untagged)` commands in the Per Agent breakdown.** Direct
  `ctx-wire run` commands are attributed by walking the process tree with `ps`,
  and some sandboxes (Codex's, for example) block `ps`, so the agent comes back
  empty. For Codex, `ctx-wire init codex` fixes this by setting
  `CTX_WIRE_AGENT = "codex"` in `shell_environment_policy.set`
  (`ctx-wire doctor` reports the state as "codex agent attribution"). For other
  wrappers, export `CTX_WIRE_AGENT=<agent>` in the environment the agent uses
  for shell commands.
- **A rules-based agent still runs raw commands.** Cline, Windsurf, Kilo Code,
  and Antigravity use rules files, not terminal hooks. They improve default
  agent behavior, but the agent can still ignore the guidance. Use
  `ctx-wire explain <cmd>` and explicit `ctx-wire run ...` when you need
  certainty.
- **A hook-capable agent (Claude, Cursor, Codex, Gemini, Copilot, OpenCode, Pi,
  Hermes) stopped filtering after an upgrade.** The shim no longer auto-wires
  under these agents, they are covered by their own hook or plugin, so if that
  hook/plugin is not installed or not active you now get no coverage instead of
  the old silent shim fallback. Run `ctx-wire doctor`: the hooks section shows
  whether the agent's hook is configured; if it says "not configured", run
  `ctx-wire init <agent>` (and for OpenCode/Pi/Hermes enable the plugin in the
  agent's own config). To force broad shim coverage regardless, set
  `CTX_WIRE_SHIMS=1`.
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
- **Commands that hide another command are passed through, not rewritten.** A
  command with command/process substitution (`$(...)`, backticks, `<(...)`), or a
  second command smuggled in via a newline or a background `&`, is left unwrapped
  so the host agent evaluates the original itself. ctx-wire will not auto-approve
  or filter a command whose embedded command it cannot attest, so e.g.
  `git log --pretty=$(...)` runs raw rather than through a filter. Plain variable
  expansion (`$VAR`, `${VAR}`) and fd redirects (`2>&1`) are unaffected.
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
