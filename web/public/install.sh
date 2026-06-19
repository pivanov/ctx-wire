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

# Verify the SHA-256 before extracting. Mirrors install.ps1 and `ctx-wire update`:
# pin CTX_WIRE_EXPECTED_SHA256 for a fleet, otherwise fetch the release's published
# <asset>.sha256. A mismatch refuses to install; CTX_WIRE_SKIP_CHECKSUM=1 bypasses
# (not recommended).
expected="${CTX_WIRE_EXPECTED_SHA256:-}"
if [ "${CTX_WIRE_SKIP_CHECKSUM:-0}" = "1" ]; then
  say "Warning: CTX_WIRE_SKIP_CHECKSUM=1 — skipping checksum verification."
else
  if [ -z "$expected" ]; then
    if curl -fsSL "$url.sha256" -o "$tmp/$asset.sha256" 2>/dev/null; then
      expected=$(awk '{print $1; exit}' "$tmp/$asset.sha256")
    else
      # Fail closed, matching `ctx-wire update`: refuse rather than install an
      # unverified binary. Every real release ships the .sha256, so this only
      # fires on a transient fetch failure or an explicit bypass.
      err "could not download the published checksum: $url.sha256 (retry, or set CTX_WIRE_SKIP_CHECKSUM=1 to install unverified at your own risk)"
    fi
  fi
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
    else
      err "no SHA-256 tool found (need sha256sum or shasum); set CTX_WIRE_SKIP_CHECKSUM=1 to bypass at your own risk"
    fi
    expected=$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')
    actual=$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')
    [ "$actual" = "$expected" ] || err "SHA-256 mismatch: $asset may be corrupt or tampered (want $expected, got $actual)"
    say "Verified SHA-256."
  fi
fi

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
