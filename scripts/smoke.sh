#!/usr/bin/env bash
#
# End-to-end smoke test for ctx-wire.
#
# Builds the binary with version metadata, then exercises the full v1 surface
# (verify, run, gain scrubbing, trust gating, agent install, hooks, MCP) inside
# hermetic temp dirs so it never touches your real HOME or production telemetry. Exits
# non-zero if any check fails.
#
#   bash scripts/smoke.sh
#
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

WORK="$(mktemp -d)"
BIN="$WORK/ctx-wire"
HOMEDIR="$WORK/home"
mkdir -p "$HOMEDIR"
trap 'rm -rf "$WORK"' EXIT

PASS=0
FAIL=0
ok()   { printf '  PASS  %s\n' "$1"; PASS=$((PASS + 1)); }
bad()  { printf '  FAIL  %s\n' "$1"; FAIL=$((FAIL + 1)); }
step() { printf '\n== %s ==\n' "$1"; }

# cw runs the built binary with all state redirected into the workdir. It also
# neutralizes agent config-dir env vars (CLAUDE_CONFIG_DIR, CODEX_HOME) so the
# smoke never touches the real agent configuration on this machine.
cw() {
	HOME="$HOMEDIR" \
		XDG_CONFIG_HOME="$HOMEDIR/.config" \
		XDG_DATA_HOME="$HOMEDIR/.local/share" \
		CLAUDE_CONFIG_DIR="$HOMEDIR/.claude" \
		CODEX_HOME="$HOMEDIR/.codex" \
		GEMINI_HOME="$HOMEDIR/.gemini" \
		CTX_WIRE_TELEMETRY_URL="http://127.0.0.1:9/ctx-wire-smoke" \
		CTX_WIRE_GAIN_FILE="$WORK/gain.jsonl" \
		CTX_WIRE_TEE_DIR="$WORK/tee" \
		"$BIN" "$@"
}

file_has() { # file substring label
	if [ -f "$1" ] && grep -q -- "$2" "$1"; then ok "$3"; else bad "$3 (missing $2 in $1)"; fi
}
file_lacks() { # file substring label
	if [ ! -f "$1" ] || ! grep -q -- "$2" "$1"; then ok "$3"; else bad "$3 (unexpected $2 in $1)"; fi
}

step "build with version metadata"
VER="${CTX_WIRE_VERSION:-0.0.1-smoke}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
BDATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if go build -ldflags "-X main.version=$VER -X main.commit=$COMMIT -X main.date=$BDATE" -o "$BIN" ./cmd/ctx-wire; then
	ok "go build"
else
	bad "go build"
	echo "build failed; aborting"
	exit 1
fi
if "$BIN" version | grep -q "$VER"; then ok "version reports $VER"; else bad "version metadata"; fi

step "verify (filter conformance)"
if cw verify | grep -q "0 failed"; then ok "ctx-wire verify"; else bad "ctx-wire verify"; fi

step "run samples"
if [ "$(cw run printf 'hello\n')" = "hello" ]; then ok "run printf"; else bad "run printf"; fi
if command -v go >/dev/null 2>&1; then
	if cw run go version >/dev/null; then ok "run go version"; else bad "run go version"; fi
fi
if command -v dotnet >/dev/null 2>&1; then
	if cw run dotnet --version >/dev/null; then ok "run dotnet --version"; else bad "run dotnet --version"; fi
else
	echo "  SKIP  run dotnet (not installed)"
fi
# Exit-code propagation end-to-end at the built binary: a wrapped child's exit
# code must reach the caller unchanged, and a missing command must exit 127.
cw run sh -c 'exit 3' >/dev/null 2>&1
rc=$?
if [ "$rc" -eq 3 ]; then ok "run propagates child exit code"; else bad "run exit code (want 3, got $rc)"; fi
cw run ctx-wire-smoke-missing-cmd >/dev/null 2>&1
rc=$?
if [ "$rc" -eq 127 ]; then ok "run missing command exits 127"; else bad "run missing command exit (want 127, got $rc)"; fi

step "gain records a scrubbed command"
rm -f "$WORK/gain.jsonl"
cw run true --password hunter2 --token abc123 >/dev/null
if [ -f "$WORK/gain.jsonl" ] && ! grep -q 'hunter2\|abc123' "$WORK/gain.jsonl" && grep -q 'REDACTED' "$WORK/gain.jsonl"; then
	ok "gain log scrubs split secret flags"
else
	bad "gain log scrubbing"
fi
if cw gain | grep -q "commands:"; then ok "ctx-wire gain summary"; else bad "ctx-wire gain summary"; fi
if cw tune | grep -q "filter improvement report"; then ok "ctx-wire tune report"; else bad "ctx-wire tune report"; fi
if cw tune --top 1 --since 24h >/dev/null; then ok "ctx-wire tune --top/--since"; else bad "ctx-wire tune --top/--since"; fi
if cw tune preview | grep -q "no network calls are made"; then ok "ctx-wire tune preview"; else bad "ctx-wire tune preview"; fi
if cw tune bundle --out "$WORK/tune-bundle.tar.gz" >/dev/null && tar -tzf "$WORK/tune-bundle.tar.gz" | grep -q "samples/commands.jsonl"; then ok "ctx-wire tune bundle"; else bad "ctx-wire tune bundle"; fi
if cw tune issue | grep -q "ctx-wire tune report"; then ok "ctx-wire tune issue"; else bad "ctx-wire tune issue"; fi
if cw discover | grep -q "ctx-wire discover"; then ok "ctx-wire discover"; else bad "ctx-wire discover"; fi
if cw telemetry status | grep -q "Aggregate telemetry:"; then ok "ctx-wire telemetry status"; else bad "ctx-wire telemetry status"; fi

step "trust-gated project filters"
PROJ="$WORK/proj"
mkdir -p "$PROJ/.ctx-wire"
cat >"$PROJ/.ctx-wire/filters.toml" <<'EOF'
schema_version = 1
[filters.echodemo]
match_command = "^echo\\b"
match_output = [{ pattern = ".*", message = "ok (project filter applied)" }]
EOF
before="$(cd "$PROJ" && cw run echo hello 2>/dev/null)"
if [ "$before" = "hello" ]; then ok "project filter ignored before trust"; else bad "project filter ignored before trust (got: $before)"; fi
if (cd "$PROJ" && cw trust >/dev/null); then ok "ctx-wire trust"; else bad "ctx-wire trust"; fi
after="$(cd "$PROJ" && cw run echo hello 2>/dev/null)"
if [ "$after" = "ok (project filter applied)" ]; then ok "project filter applied after trust"; else bad "project filter applied after trust (got: $after)"; fi

step "init agents into temp HOME/config"
cw init claude >/dev/null
if [ -x "$HOMEDIR/.local/bin/ctx-wire" ]; then ok "init claude installs binary"; else bad "init claude installs binary"; fi
# Hook/plugin-capable agents (claude) are covered by their hook, so `init` no
# longer installs PATH shims for them; shims are opt-in via `ctx-wire shims install`.
if [ ! -e "$HOMEDIR/.local/bin/git" ]; then ok "init claude skips shims (hook-capable)"; else bad "init claude skips shims (hook-capable)"; fi
cw shims install >/dev/null
if [ -x "$HOMEDIR/.local/bin/git" ] && grep -q "ctx-wire shim v1" "$HOMEDIR/.local/bin/git"; then ok "shims install"; else bad "shims install"; fi
if HOME="$HOMEDIR" \
	XDG_CONFIG_HOME="$HOMEDIR/.config" \
	XDG_DATA_HOME="$HOMEDIR/.local/share" \
	CLAUDE_CONFIG_DIR="$HOMEDIR/.claude" \
	CODEX_HOME="$HOMEDIR/.codex" \
	GEMINI_HOME="$HOMEDIR/.gemini" \
	CTX_WIRE_TELEMETRY_URL="http://127.0.0.1:9/ctx-wire-smoke" \
	CTX_WIRE_GAIN_FILE="$WORK/gain.jsonl" \
	CTX_WIRE_TEE_DIR="$WORK/tee" \
	PATH="$HOMEDIR/.local/bin:$PATH" \
	"$HOMEDIR/.local/bin/git" --version >/dev/null; then ok "shim bypasses normal git"; else bad "shim bypasses normal git"; fi
if HOME="$HOMEDIR" \
	XDG_CONFIG_HOME="$HOMEDIR/.config" \
	XDG_DATA_HOME="$HOMEDIR/.local/share" \
	CLAUDE_CONFIG_DIR="$HOMEDIR/.claude" \
	CODEX_HOME="$HOMEDIR/.codex" \
	GEMINI_HOME="$HOMEDIR/.gemini" \
	CTX_WIRE_TELEMETRY_URL="http://127.0.0.1:9/ctx-wire-smoke" \
	CTX_WIRE_GAIN_FILE="$WORK/gain.jsonl" \
	CTX_WIRE_TEE_DIR="$WORK/tee" \
	CTX_WIRE_AGENT_SHIMS=1 \
	PATH="$HOMEDIR/.local/bin:$PATH" \
	"$HOMEDIR/.local/bin/git" --version >/dev/null; then ok "shim routes agent git"; else bad "shim routes agent git"; fi
if cw doctor --recent 0 | grep -q "shim capture"; then ok "doctor reports shim usage"; else bad "doctor reports shim usage"; fi
cw init claude >/dev/null
file_has "$HOMEDIR/.claude/settings.json" "ctx-wire hook claude" "init claude"
# Re-running init keeps the explicitly-installed shims and never re-adds them for
# a hook-capable agent on its own.
if [ -x "$HOMEDIR/.local/bin/git" ] && grep -q "ctx-wire shim v1" "$HOMEDIR/.local/bin/git"; then ok "init claude keeps existing shims"; else bad "init claude keeps existing shims"; fi
cw init cursor >/dev/null
file_has "$HOMEDIR/.cursor/hooks.json" "ctx-wire hook cursor" "init cursor"
cw init codex >/dev/null
file_has "$HOMEDIR/.codex/hooks.json" "ctx-wire hook codex" "init codex"
cw init gemini >/dev/null
file_has "$HOMEDIR/.gemini/settings.json" "ctx-wire-hook-gemini.sh" "init gemini settings"
file_has "$HOMEDIR/.gemini/hooks/ctx-wire-hook-gemini.sh" "ctx-wire hook gemini" "init gemini hook"
( cd "$WORK" && cw init cline >/dev/null )
file_has "$WORK/.clinerules" "ctx-wire run git status" "init cline"
( cd "$WORK" && cw init windsurf >/dev/null )
file_has "$WORK/.windsurfrules" "ctx-wire run git status" "init windsurf"
( cd "$WORK" && cw init copilot >/dev/null )
file_has "$WORK/.github/copilot-instructions.md" "ctx-wire run git status" "init copilot instructions"
file_has "$WORK/.github/hooks/ctx-wire-rewrite.json" "ctx-wire hook copilot" "init copilot hook"
( cd "$WORK" && cw init vscode >/dev/null )
file_has "$WORK/.vscode/mcp.json" "ctx-wire" "init vscode"
cw init visualstudio >/dev/null
file_has "$HOMEDIR/.mcp.json" "ctx-wire" "init visualstudio"

step "uninstall removes ctx-wire-owned files/entries and purges its dirs"
# Seed ctx-wire's own config + data dirs so the wholesale purge has something to
# remove (telemetry is off in smoke, so these would not exist otherwise).
mkdir -p "$HOMEDIR/.config/ctx-wire" "$HOMEDIR/.local/share/ctx-wire"
: > "$HOMEDIR/.config/ctx-wire/telemetry.json"
: > "$HOMEDIR/.local/share/ctx-wire/telemetry-state.json"
( cd "$WORK" && cw uninstall >/dev/null )
if [ ! -e "$HOMEDIR/.local/bin/ctx-wire" ]; then ok "uninstall self"; else bad "uninstall self"; fi
if [ ! -e "$HOMEDIR/.local/bin/git" ]; then ok "uninstall shims"; else bad "uninstall shims"; fi
file_lacks "$HOMEDIR/.claude/settings.json" "ctx-wire" "uninstall claude hook"
file_lacks "$HOMEDIR/.cursor/hooks.json" "ctx-wire" "uninstall cursor hook"
file_lacks "$HOMEDIR/.codex/hooks.json" "ctx-wire" "uninstall codex hook"
file_lacks "$HOMEDIR/.gemini/settings.json" "ctx-wire" "uninstall gemini settings"
if [ ! -e "$HOMEDIR/.gemini/hooks/ctx-wire-hook-gemini.sh" ]; then ok "uninstall gemini hook"; else bad "uninstall gemini hook"; fi
file_lacks "$WORK/.clinerules" "ctx-wire" "uninstall cline rules"
file_lacks "$WORK/.windsurfrules" "ctx-wire" "uninstall windsurf rules"
file_lacks "$WORK/.github/copilot-instructions.md" "ctx-wire" "uninstall copilot instructions"
if [ ! -e "$WORK/.github/hooks/ctx-wire-rewrite.json" ]; then ok "uninstall copilot hook"; else bad "uninstall copilot hook"; fi
file_lacks "$WORK/.vscode/mcp.json" "ctx-wire" "uninstall vscode mcp"
file_lacks "$HOMEDIR/.mcp.json" "ctx-wire" "uninstall visualstudio mcp"
if [ ! -e "$HOMEDIR/.config/ctx-wire" ]; then ok "uninstall purges config dir"; else bad "uninstall purges config dir"; fi
if [ ! -e "$HOMEDIR/.local/share/ctx-wire" ]; then ok "uninstall purges data dir"; else bad "uninstall purges data dir"; fi

step "hook adapters rewrite commands"
claude_out="$(echo '{"tool_name":"Bash","tool_input":{"command":"git status"}}' | cw hook claude)"
if echo "$claude_out" | grep -q 'ctx-wire run --agent claude git status'; then ok "hook claude rewrite"; else bad "hook claude rewrite (got: $claude_out)"; fi
cursor_out="$(echo '{"tool_name":"Shell","tool_input":{"command":"git status"}}' | cw hook cursor)"
if echo "$cursor_out" | grep -q '"updated_input"' && echo "$cursor_out" | grep -q 'ctx-wire run --agent cursor git status'; then ok "hook cursor rewrite"; else bad "hook cursor rewrite (got: $cursor_out)"; fi
codex_out="$(echo '{"tool_name":"Bash","tool_input":{"command":"git status"}}' | cw hook codex)"
if echo "$codex_out" | grep -q 'ctx-wire run --agent codex git status'; then ok "hook codex rewrite"; else bad "hook codex rewrite (got: $codex_out)"; fi
gemini_out="$(echo '{"tool_name":"run_shell_command","tool_input":{"command":"git status"}}' | cw hook gemini)"
if echo "$gemini_out" | grep -q 'ctx-wire run --agent gemini git status'; then ok "hook gemini rewrite"; else bad "hook gemini rewrite (got: $gemini_out)"; fi
copilot_out="$(echo '{"tool_name":"runTerminalCommand","tool_input":{"command":"git status"}}' | cw hook copilot)"
if echo "$copilot_out" | grep -q 'ctx-wire run --agent copilot git status'; then ok "hook copilot vscode rewrite"; else bad "hook copilot vscode rewrite (got: $copilot_out)"; fi
copilot_cli_out="$(echo '{"toolName":"bash","toolArgs":"{\"command\":\"git status\"}"}' | cw hook copilot)"
if echo "$copilot_cli_out" | grep -q '"permissionDecision":"deny"' && echo "$copilot_cli_out" | grep -q 'ctx-wire run --agent copilot git status'; then ok "hook copilot cli suggestion"; else bad "hook copilot cli suggestion (got: $copilot_cli_out)"; fi

step "MCP run_command"
if command -v go >/dev/null 2>&1; then
	if go test ./internal/mcpserver/ -run 'TestMCPRunCommand|TestMCPLists' -count=1 >/dev/null 2>&1; then
		ok "MCP run_command (in-memory client)"
	else
		bad "MCP run_command"
	fi
else
	echo "  SKIP  MCP test (go not installed)"
fi

printf '\n== summary: %d passed, %d failed ==\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
