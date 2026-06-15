# ctx-wire

**Stop paying tokens for noisy command output.**

AI coding agents run constant shell commands, build, test, install, lint,
search, git, and read every line of the output into their context. Most of it is
noise you pay tokens for. ctx-wire sits between the agent and the shell: it
compresses that output with declarative filters, scrubs secrets, and hands back
a short result, while keeping the full (scrubbed) log on disk for when a command
fails. Wire it into your agent once; from then on it works transparently.

**[ctx-wire.dev](https://ctx-wire.dev)**

## Why ctx-wire

- **Automatic token savings.** The noisiest commands, package installs, test
  runs, linters, searches, collapse to the signal the agent actually needs.
- **Transparent.** One `ctx-wire init <agent>` and commands are filtered through
  the agent's hook (or PATH shims for steering-only agents). Nothing changes
  about how you or the agent work.
- **Nothing important is lost.** Output is scrubbed of secrets before the model
  sees it, the full scrubbed log is spooled to disk so failures stay debuggable,
  and valid JSON is never broken mid-structure.
- **Measurable.** `ctx-wire gain` reports exactly how many bytes and tokens were
  saved, broken down by program and by agent.
- **MCP output too.** `ctx-wire init claude` automatically relays known
  snapshot-heavy MCP servers (chrome-devtools, Playwright) through
  `mcp-wrap --compress`, which measures what each tool costs in tokens and
  reduces verbose browser snapshots with the raw result spooled locally.
  Any other server can be wrapped with `ctx-wire mcp-wrap install <server>`.
- **Extensible.** Filters are declarative TOML. Pull community filters, publish
  your own, or scaffold a new one from a real command transcript.
- **Works with your stack.** Claude Code, Cursor, Codex, Gemini, GitHub Copilot,
  Cline, Windsurf, Kilo Code, and more, plus MCP for editors that prefer tools.

## Quick start

```sh
curl -fsSL https://ctx-wire.dev/install.sh | sh   # macOS/Linux, installs to ~/.local/bin
ctx-wire init claude                              # or codex, cursor, gemini, copilot, ...
ctx-wire gain                                     # watch the savings add up
```

On Windows (PowerShell), install per-user with no admin:

```powershell
irm https://ctx-wire.dev/install.ps1 | iex        # add -Machine (elevated) for machine-wide
ctx-wire init copilot                             # or vscode, visualstudio, claude, ...
```

Upgrade anytime with `ctx-wire update` (checksum-verified, atomic, with
rollback) on macOS, Linux, and Windows.

## Documentation

- **[COMMANDS.md](COMMANDS.md)** - every command, with deep dives on `explain`,
  `tune`, `doctor`, the agent integrations behind `init`, and filters & trust.
- **[CONFIGURATION.md](CONFIGURATION.md)** - `config.toml`, gain storage,
  telemetry, and environment variables.
- **[FILTERS.md](FILTERS.md)** covers writing your own filters: the TOML schema,
  the transform pipeline, inline tests, and the publish flow.
- **[TROUBLESHOOTING.md](TROUBLESHOOTING.md)** - common problems and known
  limitations.
- **[DEVELOPMENT.md](DEVELOPMENT.md)** - build from source, the `just` tasks, and
  release packaging.
- **[ctx-wire.dev](https://ctx-wire.dev)** - website and impact stats.

## Backed by

[LogicStar AI](https://logicstar.ai) · [SashiDo.io](https://www.sashido.io)
