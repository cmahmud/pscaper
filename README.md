# pscaper

Go-based public-proxy feed harvester, independent validator, scoring engine, historical exports, localhost management API, and terminal dashboard. This is the **application source** repository, not the automatically changing proxy-list repository.

## Deployment status and sources

- Latest recovered application version: **1.3.1** (with October 2026 deployment hardening).
- **118 configured, independently tested feed URLs** after replacing 19 empty, unusable, or 404 sources and adding 31 working URLs from nine additional publishers.
- Feed protocols: HTTP, HTTPS, SOCKS4, SOCKS5. Source-specific refresh schedules are retained.
- Sources are only *candidates*; a candidate is not **alive** until it passes the application's outbound-forwarding callback probe.
- At most **2,000 candidates per source fetch** by default (or up to 10,000 with per-source `max_candidates`); prevents huge aggregate lists overwhelming small servers.
- Historical proxy totals recovered from `cmahmud/proxies` (223,440 discovered, 49,559 ever-alive, 1,587 ever-Gold at restoration). Historical TXT files are under `exports/historical/` and not tracked here.
- Verified working upstream examples: [ProxyScrape](https://github.com/ProxyScrape/free-proxy-list), [Litport](https://github.com/litportnet/free-proxy-list), [Proxio](https://github.com/proxio-io/proxy-list), [Moleway](https://github.com/Moleway/Free-Proxy-List) and additional independently checked publishers in `config/sources.json`.

## Paths

```text
/home/ubuntu/pscaper/
  bin/pscaper                   compiled ARM64 app (untracked)
  cmd/pscaper/                  Go entrypoint
  internal/app/                 harvesting, validator, store, API, terminal UI
  config/sources.json           configured public-list sources
  config/pscaper.service        canonical systemd unit
  config/pscaper-nginx.conf     dedicated public-IP callback virtual host
  scripts/                      setup, audit and historical scripts
  .env                          secrets and server-specific settings (gitignored)
  data/                         snapshot, journal and local TLS cert (gitignored)
  exports/                      live and append-only historical lists (gitignored)
  logs/                         service logs (gitignored)
  recovery/                     offline backups and feed audits (gitignored)
  .toolchain/ and .build-cache/ local Go compiler and build caches (gitignored)
```

Outside the tree, only symlinks register the unit and the separate Nginx virtual host:

- `/etc/systemd/system/pscaper.service` -> `/home/ubuntu/pscaper/config/pscaper.service`
- `/etc/nginx/sites-enabled/zz-pscaper` -> `/home/ubuntu/pscaper/config/pscaper-nginx.conf`

## Operations on the VPS

```bash
cd /home/ubuntu/pscaper
sudo systemctl status pscaper
sudo systemctl restart pscaper
curl -fsS http://127.0.0.1:8788/health
curl -fsS http://127.0.0.1:8788/api/stats
curl -fsS 'http://127.0.0.1:8788/api/top?limit=10'
curl -fsS -X POST http://127.0.0.1:8788/api/action/export
python3 scripts/audit-sources.py
tail -n 30 logs/pscaper.log
```

The terminal UI is available via `set -a; source .env; set +a; ./bin/pscaper tui`. Local `go test ./...` and `go build -o bin/pscaper ./cmd/pscaper` work with the project's Go toolchain at `.toolchain/go/bin/go` when Go is not globally installed.

To regenerate and test:

```bash
cd /home/ubuntu/pscaper
export GOCACHE="$PWD/.build-cache/go-build" GOMODCACHE="$PWD/.build-cache/gomod" GOPATH="$PWD/.build-cache/gopath" GOENV=off GOTOOLCHAIN=local
.toolchain/go/bin/go test ./...
CGO_ENABLED=0 .toolchain/go/bin/go build -trimpath -ldflags='-s -w' -o bin/pscaper ./cmd/pscaper
```

## Probe and network design

The VPS cloud ingress permits 80 and 443 but not dedicated callback ports 8787/8789. Nginx has a **separate server_name matching only the VPS public IPv4**. Only paths under `/probe/` are forwarded to the local HTTP probe; all other paths in that virtual host return 404. The HTTPS callback is terminated by a self-signed **probe-only** certificate stored under `data/tls/`. Other named virtual hosts such as `files.syndrid.com` are left unchanged.

Nginx supplies `X-Real-IP` from the actual remote connection. The Go server trusts that header only when the connecting peer is loopback. Callback tokens are generated into the ignored `.env`, not committed.

The management API listens on **127.0.0.1:8788 only**. Do **not** expose it to the internet: it includes privileged harvest/export controls.

## Source-quality workflow

`python3 scripts/audit-sources.py` performs a bounded parallel download of the feed URLs and reports HTTP status plus parseable public IPv4/port samples under `recovery/feed-audit.json`. This is a **format and availability audit**, not a claim that the proxies are working. Only the app's callback validator determines the live pool. Data sources change or disappear; re-run the audit when pruning or expanding sources.

## Historical exports

- `exports/alive/`: currently passing proxies.
- `exports/gold/`: repeatedly verified, higher-scoring proxies.
- `exports/historical/all-discovered.txt`: every previously discovered proxy.
- `exports/historical/ever-alive.txt`: proxy has passed at least once.
- `exports/historical/ever-gold.txt`: proxy has qualified as Gold at least once.
- `exports/stats.json`: last export's point-in-time counters.

Automatic Git pushing is **disabled** until separate server-side GitHub credentials are supplied. Keep exported proxy lists in `cmahmud/proxies` or another dedicated data repository, not `cmahmud/pscaper`. The GitHub connector can commit application **source** from ChatGPT without writing credentials onto the server.

## Safety and limitations

Free public proxies are untrusted, unstable and may intercept unencrypted traffic. **Never route authentication, personal or financial data through them.** The engine only reads configured public feeds (no internet-wide port scans). A working-proxy count is time-sensitive. Gold requires repeat validation, so it can initially be zero even if some Alive proxies have passed.

Previous source revisions and the original v1.3.0 Git bundle remain backed up locally in `recovery/`.
