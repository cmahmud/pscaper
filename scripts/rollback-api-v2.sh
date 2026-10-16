#!/usr/bin/env bash
# Restore pscaper v1.3.2 while preserving collected data and other services.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
DIR="$ROOT/recovery/api-v2-20261016"
PREV_BIN="$DIR/pscaper.pre-api-v2"
PREV_NGX="$DIR/site-nginx.pre-api-v2.conf"
PREV_UI="$ROOT/site/backups/v132-before-intelligence-20261016.html"
test -x "$PREV_BIN" && test -s "$PREV_NGX" && test -s "$PREV_UI" || {
 echo 'Pre-upgrade backups are missing; refusing rollback' >&2;exit 2;
}
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/bin/pscaper" "$DIR/pscaper.before-rollback-$STAMP"
cp -p "$ROOT/config/pscaper-site-nginx.conf" "$DIR/nginx.before-rollback-$STAMP"
cp -p "$ROOT/site/index.html" "$DIR/dashboard.before-rollback-$STAMP.html"

# Nginx syntax is checked BEFORE the production daemon gets any changes.
cp -p "$PREV_NGX" "$ROOT/config/pscaper-site-nginx.conf"
if ! sudo nginx -t;then
 cp -p "$DIR/nginx.before-rollback-$STAMP" "$ROOT/config/pscaper-site-nginx.conf"
 echo 'Previous Nginx configuration failed syntax checks; restored upgraded configuration' >&2
 exit 3
fi

sudo systemctl stop pscaper
cp -p "$PREV_BIN" "$ROOT/bin/pscaper"
cp -p "$PREV_UI" "$ROOT/site/index.html"
sudo systemctl start pscaper

for i in $(seq 1 35);do
 if curl -fsS --max-time 3 http://127.0.0.1:8788/health >/dev/null 2>&1;then
  sudo systemctl reload nginx
  echo "Previous pscaper and dashboard restored, with the database and other services unchanged."
  exit 0
 fi
 sleep 2
done
echo 'Old binary failed health check; restoring upgraded runtime' >&2
sudo systemctl stop pscaper || true
cp -p "$DIR/pscaper.before-rollback-$STAMP" "$ROOT/bin/pscaper"
cp -p "$DIR/dashboard.before-rollback-$STAMP.html" "$ROOT/site/index.html"
cp -p "$DIR/nginx.before-rollback-$STAMP" "$ROOT/config/pscaper-site-nginx.conf"
sudo systemctl start pscaper
sudo nginx -t && sudo systemctl reload nginx
exit 4
