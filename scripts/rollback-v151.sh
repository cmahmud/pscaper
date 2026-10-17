#!/usr/bin/env bash
set -euo pipefail
ROOT=/home/ubuntu/pscaper
DIR="$ROOT/recovery/feed-quality-20261017"
for path in "$DIR/pscaper-pre-change" "$DIR/sources-pre-change.json" "$DIR/site-pre-change.html";do
 test -s "$path" || { echo "Missing backup: $path" >&2; exit 2; }
done
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/bin/pscaper" "$DIR/bin-upgraded-$stamp"
cp -p "$ROOT/config/sources.json" "$DIR/sources-upgraded-$stamp.json"
cp -p "$ROOT/site/index.html" "$DIR/site-upgraded-$stamp.html"
sudo systemctl stop pscaper
cp -p "$DIR/pscaper-pre-change" "$ROOT/bin/pscaper"
cp -p "$DIR/sources-pre-change.json" "$ROOT/config/sources.json"
cp -p "$DIR/site-pre-change.html" "$ROOT/site/index.html"
sudo systemctl start pscaper
for i in $(seq 1 40);do
 if curl -fsS --max-time 3 http://127.0.0.1:8788/health >/dev/null 2>&1;then
  echo "v1.5.0 engine, original source URLs and dashboard restored. All collected data preserved."
  exit 0
 fi
 sleep 2
done
echo "Previous binary failed health check; restoring upgraded version" >&2
sudo systemctl stop pscaper || :
cp -p "$DIR/bin-upgraded-$stamp" "$ROOT/bin/pscaper"
cp -p "$DIR/sources-upgraded-$stamp.json" "$ROOT/config/sources.json"
cp -p "$DIR/site-upgraded-$stamp.html" "$ROOT/site/index.html"
sudo systemctl start pscaper
exit 3
