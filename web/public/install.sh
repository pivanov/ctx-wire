#!/bin/sh
# ctx-wire installer.
#
#   curl -fsSL https://ctx-wire.dev/install.sh | sh
#
# Downloads the latest release binary for your OS/arch from GitHub and installs
# it to ~/.local/bin (override with CTX_WIRE_INSTALL_DIR). macOS and Linux only;
# on Windows run: irm https://ctx-wire.dev/install.ps1 | iex
# Requires the repository to be public with a published release.
set -eu

REPO="pivanov/ctx-wire"
INSTALL_DIR="${CTX_WIRE_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
err() { printf 'ctx-wire install: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar >/dev/null 2>&1 || err "tar is required"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin | linux) ;;
  *) err "unsupported OS '$os' (macOS/Linux only; on Windows run: irm https://ctx-wire.dev/install.ps1 | iex)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) err "unsupported architecture '$arch'" ;;
esac

say "Finding the latest ctx-wire release…"
tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | head -1 | cut -d '"' -f 4)
[ -n "$tag" ] || err "could not find a published release (is $REPO public with a release?)"
version="${tag#v}"

asset="ctx-wire_${version}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$tag/$asset"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "Downloading $asset …"
curl -fSL "$url" -o "$tmp/$asset" || err "download failed: $url"
tar -xzf "$tmp/$asset" -C "$tmp" || err "could not extract $asset"

bin="$tmp/ctx-wire_${version}_${os}_${arch}/ctx-wire"
[ -f "$bin" ] || err "ctx-wire binary not found in the archive"

mkdir -p "$INSTALL_DIR"
chmod 755 "$bin"
mv "$bin" "$INSTALL_DIR/ctx-wire"
# macOS: clear the quarantine flag so Gatekeeper does not block the first run.
if [ "$os" = "darwin" ]; then
  xattr -d com.apple.quarantine "$INSTALL_DIR/ctx-wire" 2>/dev/null || true
fi

say ""
say "✓ Installed ctx-wire $tag to $INSTALL_DIR/ctx-wire"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    say "  Note: $INSTALL_DIR is not on your PATH yet. Add it, e.g.:"
    say "    export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

say ""
say "Next: wire up your agent, then watch the savings:"
say "  ctx-wire init claude     # or codex, cursor, gemini, copilot, …"
say "  ctx-wire gain"
