#!/usr/bin/env bash
# Run after installing the PUBLIC deploy key in cmahmud/proxies -> Settings -> Deploy keys.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
PUB="$ROOT/data/publisher/github_ed25519.pub"
KEY="$ROOT/data/publisher/github_ed25519"
KNOWN="$ROOT/data/publisher/known_hosts"
[[ -f "$PUB" && -f "$KEY" && -f "$KNOWN" ]] || {
 echo "Project publisher key not found" >&2;exit 1;
}
export GIT_SSH_COMMAND="ssh -i $KEY -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$KNOWN -o BatchMode=yes -o ConnectTimeout=12"
check="$(ssh -T git@github.com 2>&1 || true)"
if ! grep -q 'Hi cmahmud/proxies! You.ve successfully authenticated' <<< "$check"; then
 echo "GitHub has not authorized the pscaper publisher's deploy key for cmahmud/proxies." >&2
 echo "Add this PUBLIC key under https://github.com/cmahmud/proxies/settings/keys (enable Allow write access):"
 cat "$PUB"
 exit 2
fi
for name in pscaper-publish.service pscaper-publish.timer; do
 source="$ROOT/config/$name"
 target="/etc/systemd/system/$name"
 if [[ -L "$target" && "$(readlink "$target")" == "$source" ]]; then continue; fi
 if [[ -e "$target" || -L "$target" ]]; then
   echo "Refusing to overwrite $target" >&2;exit 1
 fi
 sudo ln -s "$source" "$target"
done
sudo systemctl daemon-reload
# A failed push will abort before enabling a repeating timer.
sudo systemctl start pscaper-publish.service
sudo systemctl enable --now pscaper-publish.timer
echo "pscaper VPS publisher verified and enabled at a five-minute cadence."
