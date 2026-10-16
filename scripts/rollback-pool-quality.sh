#!/usr/bin/env bash
# Roll back this patch without touching the proxy database, nginx or source feeds.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
DIR="$ROOT/recovery/quality-pool-20261016"
OLD="$DIR/pscaper.pre-quality"
OLD_UI="$DIR/dashboard.pre-quality.html"
[[ -x "$OLD" && -s "$OLD_UI" ]] || { echo 'Backup missing; no changes made' >&2; exit 2; }
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/bin/pscaper" "$DIR/pscaper.before-rollback-$STAMP"
cp -p "$ROOT/site/index.html" "$DIR/dashboard.before-rollback-$STAMP.html"
sudo systemctl stop pscaper
cp -p "$OLD" "$ROOT/bin/pscaper"
cp -p "$OLD_UI" "$ROOT/site/index.html"
sudo systemctl start pscaper
for i in $(seq 1 40); do
 if curl --max-time 3 -fsS http://127.0.0.1:8788/health >/dev/null 2>&1;then
   echo 'Previous version restored; scraper healthy. Proxy database was not changed.'
   exit 0
 fi
 sleep 2
done
echo 'Rollback version failed health check; restoring candidate release' >&2
sudo systemctl stop pscaper
cp -p "$DIR/pscaper.before-rollback-$STAMP" "$ROOT/bin/pscaper"
cp -p "$DIR/dashboard.before-rollback-$STAMP.html" "$ROOT/site/index.html"
sudo systemctl start pscaper
exit 1
