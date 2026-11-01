#!/bin/zsh
# Drive-debug loft-front on Megafon / iPhone hotspot.
# Disconnect the GUI loft profile first (one member token).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../../.." && pwd)"
BIN="${LOFT_FRONT:-$ROOT/bin/loft-front}"
if [[ ! -x "$BIN" ]]; then
  BIN=/tmp/loft-front
fi
if [[ ! -x "$BIN" ]]; then
  echo "loft-front binary not found; build with:" >&2
  echo "  go build -o /tmp/loft-front ./clients/go/cmd/loft-front" >&2
  exit 1
fi
PROFILE="${1:-$HOME/Library/Application Support/loft/profiles/node-get.yaml}"
IFACE="${IF:-en0}"
TOKEN="$(awk -F': *' '/^token:/{print $2; exit}' "$PROFILE")"
URL="$(awk -F'"' '/url:/{print $2; exit}' "$PROFILE")"
KIND="$(awk -F': *' '/type:/{print $2; exit}' "$PROFILE")"
GETPATH="$(awk -F': *' '/path:/{print $2; exit}' "$PROFILE")"
if [[ -z "$TOKEN" || -z "$URL" ]]; then
  echo "need token and url in $PROFILE" >&2
  exit 2
fi
[[ -z "$KIND" ]] && KIND=get
ARGS=(-if "$IFACE" -drive -token "$TOKEN" -url "$URL" -type "$KIND")
if [[ -n "$GETPATH" ]]; then
  ARGS+=(-path "$GETPATH")
fi
echo "drive $BIN -if $IFACE $URL type=$KIND" >&2
echo "log ~/Library/Logs/loft-drive-*.log  (also stderr)" >&2
echo "stop GUI loft first; Ctrl-C ends the run" >&2
exec "$BIN" "${ARGS[@]}"
