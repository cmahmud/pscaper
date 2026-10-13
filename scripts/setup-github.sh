#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ $# -ne 1 || "$1" != */* ]]; then
  echo "Usage: $0 OWNER/REPO" >&2
  exit 2
fi
REPO="$1"
set -a
source "$ROOT/.env"
set +a
EXPORT_DIR="${PSCAPER_EXPORT_DIR:?missing PSCAPER_EXPORT_DIR}"
VISIBILITY="${PSCAPER_GITHUB_VISIBILITY:-private}"
case "$VISIBILITY" in public|private) ;; *) echo "PSCAPER_GITHUB_VISIBILITY must be public or private" >&2; exit 2;; esac

if ! command -v gh >/dev/null 2>&1; then
  echo "[github] Installing GitHub CLI..."
  sudo apt-get update
  sudo apt-get install -y gh
fi
if ! gh auth status -h github.com >/dev/null 2>&1; then
  echo "[github] Sign in to the GitHub account that should own $REPO"
  gh auth login -h github.com -p https -w
fi
gh auth setup-git

LOGIN="$(gh api user --jq .login)"
ACCOUNT_ID="$(gh api user --jq .id)"
AUTHOR_NAME="${PSCAPER_GIT_AUTHOR_NAME:-$LOGIN}"
AUTHOR_EMAIL="${PSCAPER_GIT_AUTHOR_EMAIL:-${ACCOUNT_ID}+${LOGIN}@users.noreply.github.com}"

REPO_EXISTS=false
if gh repo view "$REPO" >/dev/null 2>&1; then
  REPO_EXISTS=true
  ACTUAL_VIS="$(gh repo view "$REPO" --json visibility --jq '.visibility | ascii_downcase')"
  if [[ "$ACTUAL_VIS" != "$VISIBILITY" ]]; then
    echo "[github] Existing repository is $ACTUAL_VIS; requested $VISIBILITY. Leaving its visibility unchanged."
  fi
else
  if [[ "$VISIBILITY" == "public" ]]; then
    gh repo create "$REPO" --public --description "pscaper validated public proxy pool"
  else
    gh repo create "$REPO" --private --description "Private pscaper validated proxy pool"
  fi
fi

WAS_ACTIVE=false
if systemctl is-active --quiet pscaper 2>/dev/null; then
  WAS_ACTIVE=true
  sudo systemctl stop pscaper
fi
restart_on_exit() {
  if [[ "$WAS_ACTIVE" == "true" ]]; then
    sudo systemctl start pscaper >/dev/null 2>&1 || true
  fi
}
trap restart_on_exit EXIT

mkdir -p "$EXPORT_DIR"
cd "$EXPORT_DIR"
REMOTE="https://github.com/${REPO}.git"
if [[ ! -d .git ]]; then
  git init -b main
fi
if git remote get-url origin >/dev/null 2>&1; then git remote set-url origin "$REMOTE"; else git remote add origin "$REMOTE"; fi

# Migration-safe behavior: if the repository already has a main branch, restore
# its latest generated pool into the fresh VPS before pscaper restarts. The
# engine can then rebuild local discovered/EverAlive/EverGold state from the
# historical export files instead of starting history at zero.
if git ls-remote --exit-code --heads origin main >/dev/null 2>&1; then
  git fetch --depth=1 origin main
  git reset --hard origin/main
  git branch --set-upstream-to=origin/main main >/dev/null 2>&1 || true
fi

# GitHub credits profile contributions by author email. Use the authenticated
# account's ID-based noreply address unless the operator explicitly overrides it.
git config user.name "$AUTHOR_NAME"
git config user.email "$AUTHOR_EMAIL"

if ! git rev-parse --verify HEAD >/dev/null 2>&1; then
  if [[ ! -e README.md ]]; then printf '# pscaper pool\n' > README.md; fi
  git add -A
  git commit -m "initialize proxy pool"
  git push -u origin HEAD:main
else
  git push -u origin HEAD:main >/dev/null 2>&1 || true
fi

if [[ "$WAS_ACTIVE" == "true" ]]; then
  sudo systemctl start pscaper
  WAS_ACTIVE=false
  trap - EXIT
  for _ in $(seq 1 20); do
    if curl -fsS http://127.0.0.1:8788/health >/dev/null 2>&1; then break; fi
    sleep 1
  done
  curl -fsS -X POST http://127.0.0.1:8788/api/action/export >/dev/null 2>&1 || true
fi

echo
echo "GitHub upload configured: $REPO"
echo "Repository visibility: $(gh repo view "$REPO" --json visibility --jq '.visibility | ascii_downcase')"
echo "Commit author: $AUTHOR_NAME <$AUTHOR_EMAIL>"
echo "Existing repository history was preserved and the new VPS is attached to main."
