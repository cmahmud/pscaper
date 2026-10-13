#!/usr/bin/env bash
set -euo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET="${1:-/home/ubuntu/pscaper}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"

if [[ ! -f "$TARGET/.env" ]]; then
  echo "Target does not look like an existing pscaper install: $TARGET" >&2
  echo "Usage: $0 /path/to/current/pscaper" >&2
  exit 2
fi

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) PREBUILT="$SRC/bin/pscaper-linux-amd64" ;;
  aarch64|arm64) PREBUILT="$SRC/bin/pscaper-linux-arm64" ;;
  *) echo "Unsupported prebuilt architecture: $ARCH" >&2; exit 1 ;;
esac

if [[ ! -x "$PREBUILT" ]]; then
  echo "Missing prebuilt binary: $PREBUILT" >&2
  exit 1
fi

sudo systemctl stop pscaper

mkdir -p "$TARGET/backups"
cp -a "$TARGET/bin/pscaper" "$TARGET/backups/pscaper-v1.0.0-$STAMP" 2>/dev/null || true
cp -a "$TARGET/.env" "$TARGET/backups/env-$STAMP"

install -m 0755 "$PREBUILT" "$TARGET/bin/pscaper"

# Preserve all existing credentials, paths, probe token, state, and GitHub config.
# Only append the new continuous-mode knobs when they are not already present.
append_if_missing() {
  local key="$1" value="$2"
  if ! grep -q "^${key}=" "$TARGET/.env"; then
    printf '%s=%s\n' "$key" "$value" >> "$TARGET/.env"
  fi
}
append_if_missing PSCAPER_CONTINUOUS true
append_if_missing PSCAPER_CYCLE_BACKOFF_MIN 5s
append_if_missing PSCAPER_CYCLE_BACKOFF_MAX 30s

sudo systemctl start pscaper
sleep 3

echo "=== VERSION ==="
"$TARGET/bin/pscaper" version

echo
echo "=== HEALTH ==="
curl -fsS http://127.0.0.1:8788/health && echo

echo
echo "=== STATS ==="
curl -fsS http://127.0.0.1:8788/api/stats && echo

echo
echo "Upgrade complete. Existing proxy state and GitHub repo were preserved."
echo "TUI: cd $TARGET && set -a && source .env && set +a && ./bin/pscaper tui"
