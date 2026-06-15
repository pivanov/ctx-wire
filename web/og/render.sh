#!/usr/bin/env bash
# Regenerate web/public/og.png (the 1200x630 social-share / OG card) from template.html.
#
# The original OG image was rendered inline and never saved, so it silently went
# stale (142 filters / 320+ tests). This makes regen a one-command, committed step:
#
#   bash web/og/render.sh
#
# After a filter-count change: edit the two counts in template.html ("NNN filters",
# "NNN+ tests"), run this, commit the new og.png. Keep them in sync with the binary.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"

if [ ! -x "$chrome" ]; then
  echo "Chrome not found at: $chrome" >&2
  echo "Set CHROME=/path/to/chrome and re-run." >&2
  exit 1
fi

"$chrome" --headless --disable-gpu --hide-scrollbars \
  --force-device-scale-factor=1 --window-size=1200,630 \
  --virtual-time-budget=5000 --default-background-color=00000000 \
  --screenshot="$here/../public/og.png" "file://$here/template.html"

echo "wrote web/public/og.png ($(file -b "$here/../public/og.png"))"
