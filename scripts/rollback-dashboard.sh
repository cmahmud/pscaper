#!/usr/bin/env bash
# Restores the preserved October 16 dashboard in seconds; Go reads site/index.html on every request.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
VERSION="${1:-v1}"
case "$VERSION" in
 v1|original) SOURCE="$ROOT/site/backups/v1-original-20261016.html" ;;
 v2) SOURCE="$ROOT/site/backups/v2-before-flow-20261016.html" ;;
 v2-current) SOURCE="$ROOT/site/backups/v2-before-pool-trend-20261016.html" ;;
 v132) SOURCE="$ROOT/site/backups/v132-before-intelligence-20261016.html" ;;
 v142) SOURCE="$ROOT/site/backups/v142-before-quality-20261017.html" ;;
 *) echo "Usage: $0 [v1|v2|v2-current|v132|v142]" >&2; exit 2 ;;
esac
TARGET="$ROOT/site/index.html"
[[ -f "$SOURCE" ]] || { echo 'Original dashboard backup missing' >&2; exit 1; }
mkdir -p "$ROOT/recovery/ui-rollbacks"
cp -p "$TARGET" "$ROOT/recovery/ui-rollbacks/dashboard-before-rollback-$(date -u +%Y%m%dT%H%M%SZ).html"
cp "$SOURCE" "$TARGET"
echo "Dashboard $VERSION restored; verify https://proxy.syndrid.com/?v=rollback"
echo 'No service restart necessary.'
