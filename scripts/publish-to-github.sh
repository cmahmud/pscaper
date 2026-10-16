#!/usr/bin/env bash
# Credential-isolated backup publisher for cmahmud/proxies, all files project-local.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
KEY="$ROOT/data/publisher/github_ed25519"
KNOWN="$ROOT/data/publisher/known_hosts"
WORK="$ROOT/data/publisher/repository"
SRC="$ROOT/exports"
LOCK="$ROOT/data/publisher/publish.lock"
exec 9>"$LOCK"
flock -n 9 || { echo 'Publisher already running'; exit 0; }
# Safe, project-local status for the public dashboard.
publisher_result=failed
trap 'python3 "$ROOT/scripts/publisher-health.py" "$publisher_result" || true' EXIT
if [[ ! -f "$KEY" || ! -f "$KNOWN" ]]; then
 echo 'Missing project-local SSH deploy key or GitHub host fingerprint' >&2
 exit 1
fi
# Use only this repository-scoped key, never interactive auth or external credentials.
export GIT_SSH_COMMAND="ssh -i $KEY -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$KNOWN -o BatchMode=yes -o ConnectTimeout=12"
auth_output="$(ssh -T -i "$KEY" -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$KNOWN" -o BatchMode=yes -o ConnectTimeout=12 git@github.com 2>&1 || true)"
if ! grep -q 'successfully authenticated' <<< "$auth_output"; then
 echo 'Deploy key not installed in cmahmud/proxies with write access yet' >&2
 exit 2
fi
if [[ ! -d "$WORK/.git" ]]; then
 git clone -q --depth 1 --filter=blob:none --sparse git@github.com:cmahmud/proxies.git "$WORK"
 git -C "$WORK" sparse-checkout set alive gold historical
fi
git -C "$WORK" fetch -q --depth 1 origin main
git -C "$WORK" reset -q --hard origin/main
python3 "$ROOT/scripts/publish-to-github.py" "$SRC" "$WORK"
# Ignore timestamp-only changes: publish only if actual proxy lists changed.
git -C "$WORK" add -- alive gold historical
if git -C "$WORK" diff --cached --quiet; then
 publisher_result=ok
 echo 'No proxy-list changes; skipping GitHub commit'
 exit 0
fi
git -C "$WORK" add -- stats.json README.md
git -C "$WORK" -c user.name='pscaper VPS' \
 -c user.email='264518661+cmahmud@users.noreply.github.com' \
 commit -qm "pscaper pool sync $(date -u +%Y-%m-%dT%H:%M:%SZ)"
# Retry once if concurrent GitHub Actions publishing moved the head.
if ! git -C "$WORK" push -q origin HEAD:main; then
 echo 'Concurrent upstream publisher: refreshing and retrying'
 git -C "$WORK" fetch -q origin main
 git -C "$WORK" -c user.name='pscaper VPS' \
 -c user.email='264518661+cmahmud@users.noreply.github.com' \
 rebase origin/main
 git -C "$WORK" push -q origin HEAD:main
fi
publisher_result=ok
echo 'Published: '$(git -C "$WORK" rev-parse --short HEAD)
