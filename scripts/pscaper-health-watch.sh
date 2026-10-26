#!/usr/bin/env bash
# pscaper-only safety watchdog. Installed root-owned; never modifies other units.
set -euo pipefail
if ! systemctl is-active --quiet pscaper.service; then
  exit 0
fi
pid="$(systemctl show pscaper.service -p MainPID --value)"
[[ "$pid" =~ ^[0-9]+$ && "$pid" -gt 1 ]] || exit 0
age="$(ps -o etimes= -p "$pid" | tr -d ' ')"
[[ "$age" =~ ^[0-9]+$ ]] || exit 0
# Do not interrupt a legitimately loading million-record snapshot.
if (( age < 300 )); then exit 0; fi
for attempt in 1 2 3; do
  if curl -fsS --connect-timeout 1 --max-time 3       http://127.0.0.1:8788/health >/dev/null 2>&1; then
    exit 0
  fi
  if (( attempt < 3 )); then sleep 4; fi
done
now="$(date +%s)"
marker=/run/pscaper-health-watchdog.last
last=0
if [[ -f "$marker" ]]; then read -r last < "$marker" || :; fi
[[ "$last" =~ ^[0-9]+$ ]] || last=0
# Restrict recovery attempts to one every half hour if the backend is sick.
if (( now-last < 1800 )); then
  logger -t pscaper-health-watchdog "Backend unresponsive; restart cooldown active"
  exit 0
fi
printf '%s\n' "$now" > "$marker"
logger -t pscaper-health-watchdog "3 health checks failed; restart of pscaper.service only"
systemctl restart --no-block pscaper.service
