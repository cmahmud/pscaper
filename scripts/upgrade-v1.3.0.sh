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
if [[ ! -f "$SRC/config/sources.json" ]]; then
  echo "Missing v1.3 source catalog" >&2
  exit 1
fi

sudo systemctl stop pscaper

mkdir -p "$TARGET/backups" "$TARGET/bin" "$TARGET/config" "$TARGET/scripts"
cp -a "$TARGET/bin/pscaper" "$TARGET/backups/pscaper-pre-v1.3.0-$STAMP" 2>/dev/null || true
cp -a "$TARGET/.env" "$TARGET/backups/env-pre-v1.3.0-$STAMP"
cp -a "$TARGET/config/sources.json" "$TARGET/backups/sources-pre-v1.3.0-$STAMP.json" 2>/dev/null || true

install -m 0755 "$PREBUILT" "$TARGET/bin/pscaper"
install -m 0644 "$SRC/config/sources.json" "$TARGET/config/sources.json"

# The paid 8-vCPU/92-GB installation used the original defaults. Increase only
# exact old defaults; user-customized values are deliberately preserved.
if grep -q '^PSCAPER_SOURCE_CONCURRENCY=12$' "$TARGET/.env"; then
  sed -i 's/^PSCAPER_SOURCE_CONCURRENCY=12$/PSCAPER_SOURCE_CONCURRENCY=24/' "$TARGET/.env"
fi
if grep -q '^PSCAPER_QUEUE_SIZE=200000$' "$TARGET/.env"; then
  sed -i 's/^PSCAPER_QUEUE_SIZE=200000$/PSCAPER_QUEUE_SIZE=3000000/' "$TARGET/.env"
fi

cp -f "$SRC/README.md" "$TARGET/README.md"
cp -f "$SRC/scripts/doctor.sh" "$TARGET/scripts/doctor.sh" 2>/dev/null || true
cp -f "$SRC/scripts/upgrade-v1.3.0.sh" "$TARGET/scripts/upgrade-v1.3.0.sh"
chmod +x "$TARGET/scripts/"*.sh 2>/dev/null || true

FEEDS="$(grep -c '"name"' "$TARGET/config/sources.json" || true)"
echo "=== SOURCE CATALOG ==="
echo "configured endpoint feeds: $FEEDS"
echo "old catalog backup: $TARGET/backups/sources-pre-v1.3.0-$STAMP.json"

echo
echo "=== EFFECTIVE HIGH-THROUGHPUT SETTINGS ==="
grep -E '^PSCAPER_(WORKERS|QUEUE_SIZE|SOURCE_CONCURRENCY)=' "$TARGET/.env" || true

sudo systemctl start pscaper
sleep 4

echo
echo "=== VERSION ==="
"$TARGET/bin/pscaper" version

echo
echo "=== HEALTH ==="
curl -fsS http://127.0.0.1:8788/health && echo

echo
echo "=== INITIAL V1.3 STATS ==="
curl -fsS http://127.0.0.1:8788/api/stats && echo

echo
echo "Upgrade complete. Existing proxy state, historical exports, GitHub authentication, repo, and probe settings were preserved."
echo "The first v1.3 cycle fetches the expanded catalog once, so DISCOVERED/QUEUED may rise sharply."
echo "TUI: cd $TARGET && set -a && source .env && set +a && ./bin/pscaper tui"
