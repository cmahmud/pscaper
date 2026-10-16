#!/usr/bin/env bash
# Daily, exclusive re-clone keeps the short-lived public-data checkout compact.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
BASE="$ROOT/data/publisher"
WORK="$BASE/repository"
FRESH="$BASE/repository.maintenance-new"
BACKUP="$BASE/repository.maintenance-old"
exec 9>"$BASE/publish.lock"
flock -w 90 9 || { echo 'Publisher busy, postponing maintenance'; exit 1; }
if [[ -d "$FRESH" || -d "$BACKUP" ]]; then
 echo 'Existing interrupted maintenance checkout; refusing destructive cleanup' >&2;exit 2
fi
if [[ -n "$(git -C "$WORK" status --porcelain)" ]];then
 echo 'Publishing checkout has uncommitted changes; postponing maintenance' >&2;exit 2
fi
KEY="$BASE/github_ed25519"
KNOWN="$BASE/known_hosts"
export GIT_SSH_COMMAND="ssh -i $KEY -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$KNOWN -o BatchMode=yes -o ConnectTimeout=12"
git clone -q --depth 1 --filter=blob:none --sparse git@github.com:cmahmud/proxies.git "$FRESH"
git -C "$FRESH" sparse-checkout set alive gold historical
head="$(git -C "$FRESH" rev-parse HEAD)"
remote="$(git -C "$FRESH" ls-remote origin refs/heads/main | awk '{print $1}')"
if [[ "$head" != "$remote" ]];then
 echo 'Remote changed during clone; refusing swap' >&2
 rm -rf -- "$FRESH"
 exit 3
fi
mv "$WORK" "$BACKUP"
mv "$FRESH" "$WORK"
echo "Publisher checkout replaced at ${head:0:9}; deleting old loose Git objects"
rm -rf -- "$BACKUP"
du -sh "$WORK/.git"
