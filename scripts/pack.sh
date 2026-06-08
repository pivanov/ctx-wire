#!/usr/bin/env bash
#
# Build a distributable ctx-wire archive under dist/.
#
#   VERSION=0.1.0-rc1 scripts/pack.sh
#   VERSION=0.1.0-rc1 just pack
#   GOOS=windows GOARCH=amd64 VERSION=0.1.0-rc1 scripts/pack.sh
#
# Windows targets produce a .zip with a ctx-wire.exe; every other target
# produces a .tar.gz. A .sha256 is written next to each archive.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GO="${GO:-go}"
BIN="${BIN:-ctx-wire}"
VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
GOOS="${GOOS:-$("$GO" env GOOS)}"
GOARCH="${GOARCH:-$("$GO" env GOARCH)}"
DIST_DIR="${DIST_DIR:-$ROOT/dist}"

# Windows binaries carry a .exe suffix and ship as a .zip.
EXT=""
ARCHIVE_KIND="tar.gz"
if [ "$GOOS" = "windows" ]; then
	EXT=".exe"
	ARCHIVE_KIND="zip"
fi
BINFILE="$BIN$EXT"

NAME="${BIN}_${VERSION}_${GOOS}_${GOARCH}"
STAGE="$DIST_DIR/$NAME"
ARCHIVE="$DIST_DIR/$NAME.$ARCHIVE_KIND"
CHECKSUM="$ARCHIVE.sha256"

rm -rf "$STAGE"
mkdir -p "$STAGE"

GOOS="$GOOS" GOARCH="$GOARCH" "$GO" build \
	-ldflags "-X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
	-o "$STAGE/$BINFILE" ./cmd/ctx-wire

chmod 755 "$STAGE/$BINFILE"

if [ "$GOOS" = "darwin" ] && command -v codesign >/dev/null 2>&1; then
	codesign --force --sign - "$STAGE/$BINFILE" >/dev/null 2>&1 || \
		echo "warn: ad-hoc codesign failed; continuing with unsigned binary" >&2
fi
if [ "$GOOS" = "darwin" ] && command -v xattr >/dev/null 2>&1; then
	xattr -d com.apple.quarantine "$STAGE/$BINFILE" 2>/dev/null || true
fi

cp README.md "$STAGE/README.md"
# Ship the docs the README links to, so its links resolve in the archive.
cp COMMANDS.md "$STAGE/COMMANDS.md"
cp CONFIGURATION.md "$STAGE/CONFIGURATION.md"
cp FILTERS.md "$STAGE/FILTERS.md"
cp TROUBLESHOOTING.md "$STAGE/TROUBLESHOOTING.md"
cp DEVELOPMENT.md "$STAGE/DEVELOPMENT.md"
if [ "$GOOS" = "windows" ]; then
	cat > "$STAGE/INSTALL.txt" <<EOF
ctx-wire $VERSION ($GOOS/$GOARCH)

Install:

  .\\ctx-wire.exe init

Verify:

  ctx-wire.exe version
  ctx-wire.exe verify
  ctx-wire.exe doctor

Notes:

  init installs ctx-wire and sets up the agent hooks/shims.
  If ctx-wire is not found afterward, add its install directory to PATH.
EOF
else
	cat > "$STAGE/INSTALL.txt" <<EOF
ctx-wire $VERSION ($GOOS/$GOARCH)

Install:

  xattr -d com.apple.quarantine ./ctx-wire 2>/dev/null || true
  ./ctx-wire init claude    # or codex, cursor, gemini, ...

Verify:

  ctx-wire version
  ctx-wire verify
  ctx-wire doctor

Notes:

  init <agent> copies this binary to ~/.local/bin/ctx-wire (chmod 755), adds
  managed shims, and wires that agent. If ctx-wire is not found after install,
  add ~/.local/bin to PATH.
EOF
fi

rm -f "$ARCHIVE" "$CHECKSUM"
if [ "$ARCHIVE_KIND" = "zip" ]; then
	if command -v zip >/dev/null 2>&1; then
		( cd "$DIST_DIR" && zip -q -r "$(basename "$ARCHIVE")" "$NAME" )
	else
		# Fall back to .tar.gz when zip is unavailable; Windows 10+ ships tar.
		echo "warn: zip not found; writing .tar.gz instead" >&2
		ARCHIVE="$DIST_DIR/$NAME.tar.gz"
		CHECKSUM="$ARCHIVE.sha256"
		rm -f "$ARCHIVE" "$CHECKSUM"
		tar -C "$DIST_DIR" -czf "$ARCHIVE" "$NAME"
	fi
else
	tar -C "$DIST_DIR" -czf "$ARCHIVE" "$NAME"
fi

(
	cd "$DIST_DIR"
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$(basename "$ARCHIVE")" > "$(basename "$CHECKSUM")"
	elif command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$(basename "$ARCHIVE")" > "$(basename "$CHECKSUM")"
	else
		echo "warn: no sha256 tool found; checksum not written" >&2
	fi
)

rm -rf "$STAGE"

echo "wrote $ARCHIVE"
if [ -f "$CHECKSUM" ]; then
	echo "wrote $CHECKSUM"
fi
