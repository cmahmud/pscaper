#!/usr/bin/env bash
set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
set -a; source "$ROOT/.env"; set +a
printf '=== SERVICE ===\n'
systemctl --no-pager --full status pscaper | sed -n '1,18p'
printf '\n=== LIMITS ===\n'
printf 'shell nofile: '; ulimit -n
printf 'service nofile: '; systemctl show pscaper -p LimitNOFILE --value
printf '\n=== LISTENERS ===\n'
ss -lntp 2>/dev/null | grep -E ':(8787|8788|8789)\b' || true
printf '\n=== HEALTH ===\n'
curl -fsS http://127.0.0.1:8788/health || true; echo
printf '\n=== STATS ===\n'
curl -fsS http://127.0.0.1:8788/api/stats || true; echo
printf '\n=== EXPORT REPO ===\n'
if [[ -d "${PSCAPER_EXPORT_DIR}/.git" ]]; then
  git -C "$PSCAPER_EXPORT_DIR" remote -v
  git -C "$PSCAPER_EXPORT_DIR" status --short --branch
else
  echo 'GitHub export repo not configured yet.'
fi
