#!/usr/bin/env bash
# Restore last known good v1.4.0 engine, leaving Nginx, UI and proxy DB intact.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
DIR="$ROOT/recovery/api-v2-20261016"
OLD="$DIR/pscaper.v14-before-bogon"
test -x "$OLD" || { echo 'v1.4.0 backup missing' >&2; exit 2; }
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/bin/pscaper" "$DIR/pscaper.v141-before-rollback-$STAMP"
sudo systemctl stop pscaper
cp -p "$OLD" "$ROOT/bin/pscaper"
sudo systemctl start pscaper
for i in $(seq 1 40);do
 if curl -fsS --max-time 3 http://127.0.0.1:8788/health >/dev/null 2>&1;then
  echo 'v1.4.0 restored; Nginx/site/database remain unchanged'
  exit 0
 fi
 sleep 2
done
echo 'Rollback failed; restoring v1.4.1 executable' >&2
sudo systemctl stop pscaper || :
cp -p "$DIR/pscaper.v141-before-rollback-$STAMP" "$ROOT/bin/pscaper"
sudo systemctl start pscaper
exit 4
