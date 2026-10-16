#!/usr/bin/env bash
# Restore the pre-optimization release without changing other VPS services.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
BACKUP="$ROOT/recovery/optimization-20261016/pscaper-pre-opt"
CURRENT="$ROOT/bin/pscaper"
SAVED="$ROOT/recovery/optimization-20261016/pscaper-before-rollback-$(date -u +%Y%m%dT%H%M%SZ)"
[[ -x "$BACKUP" ]] || { echo 'Original binary missing' >&2; exit 2; }
[[ -x "$CURRENT" ]] || { echo 'Running release binary missing' >&2; exit 2; }
cp -p "$CURRENT" "$SAVED"
sudo systemctl stop pscaper
cp -p "$BACKUP" "$CURRENT"
sudo systemctl start pscaper
for i in $(seq 1 40);do
 if curl -fsS --max-time 3 http://127.0.0.1:8788/health >/dev/null 2>&1;then
   echo 'pscaper previous version restored and healthy. New binary preserved at' "$SAVED"
   exit 0
 fi
 sleep 2
done
echo 'Old binary did not start; attempting to restore current version' >&2
sudo systemctl stop pscaper
cp -p "$SAVED" "$CURRENT"
sudo systemctl start pscaper
exit 1
