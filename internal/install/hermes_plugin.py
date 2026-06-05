"""Hermes plugin adapter for ctx-wire command rewriting.

All rewrite logic lives in ``ctx-wire rewrite``; this module only bridges Hermes
``pre_tool_call`` payloads to that command and fails open (never blocks a tool
call on an error).
"""

import shutil
import subprocess
import sys

_REWRITE_TIMEOUT_S = 2.0


def register(ctx):
    """Register the Hermes pre-tool callback when ctx-wire is available."""
    if shutil.which("ctx-wire") is None:
        sys.stderr.write("[ctx-wire] binary not found in PATH; Hermes hook not registered\n")
        return
    ctx.register_hook("pre_tool_call", _pre_tool_call)


def _pre_tool_call(tool_name=None, args=None, **_kwargs):
    """Rewrite the mutable Hermes terminal command when ctx-wire changes it."""
    try:
        if tool_name != "terminal" or not isinstance(args, dict):
            return
        command = args.get("command")
        if not isinstance(command, str) or not command.strip():
            return
        if command.startswith("ctx-wire "):
            return

        result = subprocess.run(
            ["ctx-wire", "rewrite", command],
            capture_output=True,
            text=True,
            timeout=_REWRITE_TIMEOUT_S,
        )
        if result.returncode != 0:
            return
        rewritten = result.stdout.strip()
        if rewritten and rewritten != command:
            args["command"] = rewritten
    except Exception:
        # Fail open: never block execution on an unexpected error.
        return
