#!/usr/bin/env bash
# Restores the preserved October 16 dashboard in seconds; Go reads site/index.html on every request.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
SOURCE="$ROOT/site/backups/v1-original-20261016.html"
TARGET="$ROOT/site/index.html"
[[ -f "$SOURCE" ]] || { echo 'Original dashboard backup missing' >&2; exit 1; }
mkdir -p "$ROOT/recovery/ui-rollbacks"
cp -p "$TARGET" "$ROOT/recovery/ui-rollbacks/dashboard-before-rollback-$(date -u +%Y%m%dT%H%M%SZ).html"
cp "$SOURCE" "$TARGET"
echo 'Original dashboard restored; verify https://proxy.syndrid.com/?v=rollback'
echo 'No service restart necessary.'
