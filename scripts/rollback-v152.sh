#!/usr/bin/env bash
# Restore v1.5.1 release and its exact 329-feed configuration.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
DIR="$ROOT/recovery/feed-scale-20261017"
OLD="$DIR/pscaper.old"
OLD_SRC="$DIR/sources.old.json"
[[ -x "$OLD" && -s "$OLD_SRC" ]] || { echo 'Backups missing: no changes made' >&2; exit 2; }
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/bin/pscaper" "$DIR/pscaper.before-rollback-$stamp"
cp -p "$ROOT/config/sources.json" "$DIR/sources.before-rollback-$stamp.json"
sudo systemctl stop pscaper
cp -p "$OLD" "$ROOT/bin/pscaper"
cp -p "$OLD_SRC" "$ROOT/config/sources.json"
sudo systemctl start pscaper
for i in $(seq 1 40);do
 if curl -fsS --max-time 3 http://127.0.0.1:8788/health >/dev/null 2>&1;then
    echo 'v1.5.1 engine and 329 original sources restored; existing proxy data preserved.'
    exit 0
 fi
 sleep 2
done
echo 'Old version failed health checks; restoring upgraded release' >&2
sudo systemctl stop pscaper || true
cp -p "$DIR/pscaper.before-rollback-$stamp" "$ROOT/bin/pscaper"
cp -p "$DIR/sources.before-rollback-$stamp.json" "$ROOT/config/sources.json"
sudo systemctl start pscaper
exit 4
