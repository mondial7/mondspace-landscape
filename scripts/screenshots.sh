#!/usr/bin/env bash
# Regenerates docs/images by running landscape on its own repository and
# capturing the views with headless Chrome — the README diagrams are made
# by the tool itself.
#
#   scripts/screenshots.sh [repo] [out-dir]
set -euo pipefail

REPO="${1:-.}"
OUT="${2:-docs/images}"
PORT="${PORT:-7999}"
CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
BIN="${BIN:-$(go env GOPATH)/bin/landscape}"
TMP="$(mktemp -d)"
trap 'kill "${PID:-}" 2>/dev/null || true; rm -rf "$TMP"' EXIT

mkdir -p "$OUT"
"$BIN" --no-open --port "$PORT" "$REPO" >"$TMP/log" 2>&1 &
PID=$!

for _ in $(seq 1 50); do grep -q "landscape: http" "$TMP/log" 2>/dev/null && break; sleep 0.2; done
ID="$(grep -o '#/a/[a-z0-9]*' "$TMP/log" | head -1 | cut -c5-)"
[ -n "$ID" ] || { cat "$TMP/log"; echo "server did not start" >&2; exit 1; }

echo "waiting for analysis $ID (descriptions come from your local model, if any)…"
for _ in $(seq 1 240); do
  STATUS="$(curl -s "http://127.0.0.1:$PORT/api/analyses/$ID" | sed -n 's/.*"status":"\([a-z]*\)".*/\1/p')"
  [ "$STATUS" = ready ] && break
  [ "$STATUS" = failed ] && { echo "analysis failed" >&2; exit 1; }
  sleep 2
done

shoot() { # name focus scheme(dark|light)
  local flag=()
  [ "$3" = light ] && flag=(--blink-settings=preferredColorScheme=1) || flag=(--force-dark-mode --blink-settings=preferredColorScheme=0)
  "$CHROME" --headless=new --disable-gpu --hide-scrollbars --window-size=1600,1000 \
    --virtual-time-budget=6000 --user-data-dir="$TMP/chrome" "${flag[@]}" \
    --screenshot="$OUT/$1-$3.png" "http://127.0.0.1:$PORT/#/a/$ID${2:+/$2}" >/dev/null 2>&1
  echo "  $OUT/$1-$3.png"
}

for scheme in dark light; do
  shoot context "" "$scheme"
  shoot containers "sys" "$scheme"
  shoot components "ctr%3Acmd%2Flandscape" "$scheme"
  shoot code "cmp%3Ainternal%2Flandscape" "$scheme"
done
