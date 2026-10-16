#!/usr/bin/env bash
# Revert v1.4.2 startup-only change to v1.4.1; website/database unaffected.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
DIR="$ROOT/recovery/api-v2-20261016"
OLD="$DIR/pscaper.v141-before-hotfix"
test -x "$OLD" || { echo 'v1.4.1 backup missing' >&2;exit 2; }
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/bin/pscaper" "$DIR/pscaper.v142-pre-rollback-$STAMP"
sudo systemctl stop pscaper
cp -p "$OLD" "$ROOT/bin/pscaper"
sudo systemctl start pscaper
for i in $(seq 1 45);do
 if curl -fsS --max-time 3 http://127.0.0.1:8788/health >/dev/null 2>&1;then echo 'v1.4.1 running';exit 0;fi
 sleep 2
done
sudo systemctl stop pscaper || :
cp -p "$DIR/pscaper.v142-pre-rollback-$STAMP" "$ROOT/bin/pscaper"
sudo systemctl start pscaper
exit 4
