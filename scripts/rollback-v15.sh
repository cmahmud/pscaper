#!/usr/bin/env bash
set -euo pipefail
ROOT=/home/ubuntu/pscaper
BACKUP="$ROOT/recovery/v15-20261017"
test -x "$BACKUP/pscaper.v142" &&
test -s "$BACKUP/site.v142.html" &&
test -s "$BACKUP/nginx.v142.conf" || {
 echo 'Previous release files missing; no changes made' >&2
 exit 2
}
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/bin/pscaper" "$BACKUP/bin.before-rollback-$STAMP"
cp -p "$ROOT/site/index.html" "$BACKUP/site.before-rollback-$STAMP.html"
cp -p "$ROOT/config/pscaper-site-nginx.conf" "$BACKUP/nginx.before-rollback-$STAMP.conf"
cp -p "$BACKUP/nginx.v142.conf" "$ROOT/config/pscaper-site-nginx.conf"
if ! sudo nginx -t; then
 cp -p "$BACKUP/nginx.before-rollback-$STAMP.conf" "$ROOT/config/pscaper-site-nginx.conf"
 exit 3
fi
sudo systemctl stop pscaper
cp -p "$BACKUP/pscaper.v142" "$ROOT/bin/pscaper"
cp -p "$BACKUP/site.v142.html" "$ROOT/site/index.html"
sudo systemctl start pscaper
for i in $(seq 1 40); do
 if curl -fsS --max-time 3 http://127.0.0.1:8788/health >/dev/null 2>&1; then
   sudo systemctl reload nginx
   echo "v1.4.2 restored. Existing proxy data and other VPS services preserved."
   exit 0
 fi
 sleep 2
done
echo 'Rollback binary failed health check; restoring upgraded release' >&2
sudo systemctl stop pscaper || true
cp -p "$BACKUP/bin.before-rollback-$STAMP" "$ROOT/bin/pscaper"
cp -p "$BACKUP/site.before-rollback-$STAMP.html" "$ROOT/site/index.html"
cp -p "$BACKUP/nginx.before-rollback-$STAMP.conf" "$ROOT/config/pscaper-site-nginx.conf"
sudo systemctl start pscaper
sudo nginx -t && sudo systemctl reload nginx
exit 4
