#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USER_NAME="${SUDO_USER:-$USER}"
USER_HOME="$(getent passwd "$USER_NAME" | cut -d: -f6)"
EXPORT_DIR="$ROOT/exports"

mkdir -p "$ROOT/bin" "$ROOT/data" "$EXPORT_DIR"
PUBLIC_IP="$(curl -4 -fsS --max-time 8 https://api.ipify.org || true)"
if [[ -z "$PUBLIC_IP" ]]; then
  PUBLIC_IP="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1)"
fi
if [[ -z "$PUBLIC_IP" ]]; then
  echo "Could not determine the VPS public IPv4. Set PSCAPER_PUBLIC_HOST manually in $ROOT/.env" >&2
  PUBLIC_IP="CHANGE_ME"
fi
TOKEN="$(openssl rand -hex 18 2>/dev/null || head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')"

cat > "$ROOT/.env" <<ENV
PSCAPER_PUBLIC_HOST=$PUBLIC_IP
# Conservative defaults for the permanent 2-OCPU / 12-GB ARM VPS.
# They are intentionally I/O-friendly rather than trying to reproduce the old 96-GB host's concurrency.
PSCAPER_WORKERS=600
PSCAPER_QUEUE_SIZE=100000
PSCAPER_TIMEOUT=6s
PSCAPER_SOURCE_CONCURRENCY=8
PSCAPER_CONTINUOUS=true
PSCAPER_CYCLE_BACKOFF_MIN=5s
PSCAPER_CYCLE_BACKOFF_MAX=30s
PSCAPER_HARVEST_INTERVAL=5m
PSCAPER_ALIVE_RECHECK=5m
PSCAPER_DEAD_RECHECK=30m
PSCAPER_EXPORT_INTERVAL=10m
PSCAPER_ARCHIVE_INTERVAL=1h
PSCAPER_FLUSH_INTERVAL=3s
PSCAPER_GOLD_SCORE=80
PSCAPER_API_ADDR=127.0.0.1:8788
PSCAPER_PROBE_HTTP_ADDR=:8787
PSCAPER_PROBE_HTTPS_ADDR=:8789
PSCAPER_PROBE_HTTP_PORT=8787
PSCAPER_PROBE_HTTPS_PORT=8789
PSCAPER_PROBE_TOKEN=$TOKEN
PSCAPER_DATA_DIR=$ROOT/data
PSCAPER_SOURCES=$ROOT/config/sources.json
PSCAPER_EXPORT_DIR=$EXPORT_DIR
PSCAPER_GIT_PUSH=true
PSCAPER_GIT_REMOTE=origin
PSCAPER_GIT_BRANCH=main
ENV
chmod 600 "$ROOT/.env"

cd "$ROOT"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) PREBUILT="$ROOT/bin/pscaper-linux-amd64" ;;
  aarch64|arm64) PREBUILT="$ROOT/bin/pscaper-linux-arm64" ;;
  *) PREBUILT="" ;;
esac
if [[ -n "$PREBUILT" && -x "$PREBUILT" ]]; then
  cp "$PREBUILT" "$ROOT/bin/pscaper"
else
  if ! command -v go >/dev/null 2>&1; then
    echo "No matching prebuilt binary and Go is not installed." >&2
    echo "Install Go 1.22+ and re-run this script." >&2
    exit 1
  fi
  go test ./...
  go build -trimpath -ldflags='-s -w' -o "$ROOT/bin/pscaper" ./cmd/pscaper
fi
chmod +x "$ROOT/bin/pscaper"
"$ROOT/bin/pscaper" version

if command -v ufw >/dev/null 2>&1 && sudo ufw status 2>/dev/null | grep -q '^Status: active'; then
  sudo ufw allow 8787/tcp >/dev/null
  sudo ufw allow 8789/tcp >/dev/null
fi

SERVICE=/etc/systemd/system/pscaper.service
sudo tee "$SERVICE" >/dev/null <<UNIT
[Unit]
Description=pscaper public proxy harvester and validator
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$USER_NAME
WorkingDirectory=$ROOT
EnvironmentFile=$ROOT/.env
ExecStart=$ROOT/bin/pscaper serve
Restart=always
RestartSec=2
LimitNOFILE=262144
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=false
ReadWritePaths=$ROOT $EXPORT_DIR

[Install]
WantedBy=multi-user.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now pscaper
sleep 2

echo
echo "=== pscaper installed ==="
echo "Project:     $ROOT"
echo "Public host: $PUBLIC_IP"
echo "Exports:     $EXPORT_DIR"
echo "TUI:         cd $ROOT && set -a && source .env && set +a && ./bin/pscaper tui"
echo
echo "IMPORTANT: TCP 8787 and 8789 must also be allowed in your VPS provider/cloud firewall."
echo "Local health: curl -fsS http://127.0.0.1:8788/health"
echo "GitHub setup: $ROOT/scripts/setup-github.sh OWNER/PRIVATE_REPO"
