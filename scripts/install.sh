#!/usr/bin/env bash
# Self-contained installer/reinstaller for the known 2-core pscaper VPS.
# Requires existing .env and Nginx, and never creates sibling projects.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ "$ROOT" == /home/ubuntu/pscaper ]] || { echo "Expected /home/ubuntu/pscaper" >&2; exit 2; }
[[ -f "$ROOT/.env" ]] || { echo "Create $ROOT/.env from .env.example and configure your public IP/token" >&2; exit 2; }
if grep -q 'CHANGE_ME_TO_LONG_RANDOM_TOKEN\|203.0.113.10' "$ROOT/.env"; then
  echo "Configure PSCAPER_PUBLIC_HOST and PSCAPER_PROBE_TOKEN first" >&2; exit 2
fi
command -v nginx >/dev/null || { echo "Nginx is required for the 80/443 callback gateway" >&2; exit 2; }
mkdir -p "$ROOT/bin" "$ROOT/data/tls" "$ROOT/exports" "$ROOT/logs"
if [[ ! -x "$ROOT/bin/pscaper" ]]; then
  GO="$(command -v go || true)"
  if [[ -x "$ROOT/.toolchain/go/bin/go" ]]; then GO="$ROOT/.toolchain/go/bin/go"; fi
  [[ -n "$GO" ]] || { echo "Install Go or unpack a project-local Go toolchain" >&2; exit 2; }
  (cd "$ROOT"; export GOCACHE="$ROOT/.build-cache/go-build" GOMODCACHE="$ROOT/.build-cache/gomod" GOPATH="$ROOT/.build-cache/gopath" GOENV=off GOTOOLCHAIN=local CGO_ENABLED=0
   "$GO" test ./... && "$GO" build -trimpath -ldflags='-s -w' -o "$ROOT/bin/pscaper" ./cmd/pscaper)
fi
if [[ ! -f "$ROOT/data/tls/probe.key" ]]; then
  openssl req -x509 -nodes -newkey rsa:2048 -sha256 -days 3650 -subj '/CN=pscaper-probe' \
    -keyout "$ROOT/data/tls/probe.key" -out "$ROOT/data/tls/probe.crt" 2>/dev/null
  chmod 600 "$ROOT/data/tls/probe.key"
fi
link_checked() {
  local source="$1" dest="$2"
  if [[ -L "$dest" && "$(readlink "$dest")" == "$source" ]]; then return; fi
  if [[ -e "$dest" || -L "$dest" ]]; then
    echo "Refusing to overwrite existing path: $dest" >&2; exit 1
  fi
  sudo ln -s "$source" "$dest"
}
link_checked "$ROOT/config/pscaper.service" /etc/systemd/system/pscaper.service
link_checked "$ROOT/config/pscaper-nginx.conf" /etc/nginx/sites-enabled/zz-pscaper
sudo nginx -t
sudo systemctl reload nginx
sudo systemctl daemon-reload
sudo systemctl enable --now pscaper
curl --retry 4 --retry-delay 2 -fsS http://127.0.0.1:8788/health
echo
echo "pscaper service deployed. All project data is in $ROOT"
