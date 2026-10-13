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

set -a
source "$TARGET/.env"
set +a
EXPORT_DIR="${PSCAPER_EXPORT_DIR:?PSCAPER_EXPORT_DIR missing from $TARGET/.env}"

sudo systemctl stop pscaper

mkdir -p "$TARGET/backups"
cp -a "$TARGET/bin/pscaper" "$TARGET/backups/pscaper-pre-v1.2.0-$STAMP" 2>/dev/null || true
cp -a "$TARGET/.env" "$TARGET/backups/env-pre-v1.2.0-$STAMP"

# Recover pre-v1.2 current-pool history from the existing private Git repo.
# This is read-only against Git history; generated unions are then committed by v1.2.
mkdir -p "$EXPORT_DIR/historical"
seed_history() {
  local kind="$1" out="$2" tmp
  tmp="$(mktemp)"
  if [[ -f "$EXPORT_DIR/$kind/all.txt" ]]; then
    cat "$EXPORT_DIR/$kind/all.txt" >> "$tmp" || true
  fi
  if [[ -d "$EXPORT_DIR/.git" ]]; then
    (
      cd "$EXPORT_DIR"
      git rev-list --all 2>/dev/null | while read -r rev; do
        git show "$rev:$kind/all.txt" 2>/dev/null || true
      done
    ) >> "$tmp"
  fi
  if [[ -f "$EXPORT_DIR/historical/$out" ]]; then
    cat "$EXPORT_DIR/historical/$out" >> "$tmp" || true
  fi
  awk 'NF' "$tmp" | LC_ALL=C sort -u > "$EXPORT_DIR/historical/$out.tmp"
  mv "$EXPORT_DIR/historical/$out.tmp" "$EXPORT_DIR/historical/$out"
  rm -f "$tmp"
}

echo "=== RECOVERING EXISTING GIT HISTORY ==="
seed_history alive ever-alive.txt
seed_history gold ever-gold.txt
echo "ever-alive recovered: $(wc -l < "$EXPORT_DIR/historical/ever-alive.txt" | tr -d ' ')"
echo "ever-gold recovered:  $(wc -l < "$EXPORT_DIR/historical/ever-gold.txt" | tr -d ' ')"

install -m 0755 "$PREBUILT" "$TARGET/bin/pscaper"

append_if_missing() {
  local key="$1" value="$2"
  if ! grep -q "^${key}=" "$TARGET/.env"; then
    printf '%s=%s\n' "$key" "$value" >> "$TARGET/.env"
  fi
}
append_if_missing PSCAPER_ARCHIVE_INTERVAL 1h

# Keep the local source/scripts useful for future maintenance when present in the upgrade bundle.
cp -f "$SRC/README.md" "$TARGET/README.md" 2>/dev/null || true
cp -f "$SRC/scripts/doctor.sh" "$TARGET/scripts/doctor.sh" 2>/dev/null || true
cp -f "$SRC/scripts/upgrade-v1.2.0.sh" "$TARGET/scripts/upgrade-v1.2.0.sh" 2>/dev/null || true

sudo systemctl start pscaper
sleep 3

# Force an early export so all-discovered + reconstructed history appear promptly.
curl -fsS -X POST http://127.0.0.1:8788/api/action/export >/dev/null 2>&1 || true
sleep 3

echo
echo "=== VERSION ==="
"$TARGET/bin/pscaper" version

echo
echo "=== HEALTH ==="
curl -fsS http://127.0.0.1:8788/health && echo

echo
echo "=== HISTORY/STATS ==="
curl -fsS http://127.0.0.1:8788/api/stats && echo

echo
echo "Upgrade complete. Existing proxy state, probe settings, GitHub authentication, and repo were preserved."
echo "Historical exports: $EXPORT_DIR/historical"
echo "TUI: cd $TARGET && set -a && source .env && set +a && ./bin/pscaper tui"
