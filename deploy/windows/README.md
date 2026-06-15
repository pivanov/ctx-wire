# ctx-wire on Windows (managed / GPO deployment)

For a single machine, the one-liner is all you need:

```powershell
irm https://ctx-wire.dev/install.ps1 | iex
ctx-wire init copilot      # or vscode, visualstudio, claude, ...
```

This folder covers the **managed/fleet** case (Group Policy), which splits into a
machine-wide install (computer startup) and per-user wiring (logon).

## 1. Install machine-wide (GPO computer startup, elevated)

`install.ps1 -Machine` installs to `%ProgramFiles%\ctx-wire\bin` and adds it to the
machine PATH. For an offline/air-gapped fleet, stage a release `.zip` on a share and
point `-SourcePath` at it (optionally pin `-ExpectedSha256`):

```powershell
# online
& ([scriptblock]::Create((irm https://ctx-wire.dev/install.ps1))) -Machine

# offline, from a share
.\install.ps1 -Machine -SourcePath \\share\ctx-wire_<version>_windows_amd64.zip
```

## 2. Wire agents per user (GPO logon)

At user logon, wire the agents and (optionally) each project root. `ctx-wire init`
writes the Copilot/VS Code/Visual Studio instructions and MCP config; `shims install`
adds the PATH-shim fallback for steering-only agents.

```powershell
ctx-wire init copilot
ctx-wire init vscode
ctx-wire init visualstudio
ctx-wire shims install
```

A logon script can loop project roots (e.g. from a policy env var like
`CTX_WIRE_PROJECT_ROOTS`, a `;`-separated list) and run `init copilot` / `init vscode`
in each.

## 3. Enable the MCP server + tools in Visual Studio

`init visualstudio` writes the MCP config to `~/.mcp.json`, but Visual Studio also
needs the server and its `read_file` / `run_command` tools enabled in each instance's
Copilot settings. [`enable-ctx-wire-visualstudio.ps1`](enable-ctx-wire-visualstudio.ps1)
does that across all VS instances at logon.

**Note the trade-off:** the helper sets `autoExecutionMode = 'Always'`, so Visual
Studio runs commands through ctx-wire without a per-call approval prompt , appropriate
for a controlled fleet, but a deliberate permission decision. It also writes
undocumented `copilot.featureFlags.*` preview settings, so re-test after a Visual
Studio major upgrade. See the script header for the full caveats.

VS Code and other editors expose the same MCP tools (`run_command`, `read_file`); the
instruction block ctx-wire writes already steers the agent to prefer them over the
built-in shell/read/grep/glob tools.
