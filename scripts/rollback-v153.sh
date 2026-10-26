#!/usr/bin/env bash
# Revert v1.5.3 engine, site and pscaper-specific Nginx config.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
BK="$ROOT/recovery/incident-20261025"
test -x "$BK/pscaper.before" && test -s "$BK/site-nginx.before" || {
 echo 'Original v1.5.2 backups not found; refusing rollback' >&2
 exit 2
}
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/bin/pscaper" "$BK/pscaper.before-rollback-$stamp"
cp -p "$ROOT/config/pscaper-site-nginx.conf" "$BK/nginx.before-rollback-$stamp"
cp -p "$ROOT/site/index.html" "$BK/site.before-rollback-$stamp.html"
sudo systemctl stop pscaper
cp -p "$BK/pscaper.before" "$ROOT/bin/pscaper"
cp -p "$BK/site-nginx.before" "$ROOT/config/pscaper-site-nginx.conf"
sudo nginx -t
sudo systemctl reload nginx
sudo systemctl start pscaper
for i in $(seq 1 55);do
 if curl -fsS --max-time 3 http://127.0.0.1:8788/health >/dev/null 2>&1;then
  echo "Previous v1.5.2 engine and site Nginx configuration restored."
  echo "Database and other VPS services remain intact."
  exit 0
 fi
 sleep 2
done
echo 'Prior version could not recover; restoring upgraded binary and Nginx' >&2
sudo systemctl stop pscaper || true
cp -p "$BK/pscaper.before-rollback-$stamp" "$ROOT/bin/pscaper"
cp -p "$BK/nginx.before-rollback-$stamp" "$ROOT/config/pscaper-site-nginx.conf"
cp -p "$BK/site.before-rollback-$stamp.html" "$ROOT/site/index.html"
sudo nginx -t && sudo systemctl reload nginx
sudo systemctl start pscaper
exit 3
