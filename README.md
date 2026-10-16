# pscaper

Go-based public-proxy feed harvester, independent validator, scoring engine, historical exports, localhost management API, and terminal dashboard. This is the **application source** repository, not the automatically changing proxy-list repository.

## Deployment status and sources

- Latest recovered application version: **1.3.1** (with October 2026 deployment hardening).
- **329 configured feed URLs**, including 203 additional audited endpoints from 83 newly represented repository/domain identifiers (previously 126). These are not 329 independent organizations, and they do not guarantee a larger Alive pool; all candidate proxies still undergo independent validation.
- Feed protocols: HTTP, HTTPS, SOCKS4, SOCKS5. Source-specific refresh schedules are retained.
- Sources are only *candidates*; a candidate is not **alive** until it passes the application's outbound-forwarding callback probe.
- Reservoir sampling inspects the bounded upstream feed and retains **2,000 representative candidates** per fetch by default (up to 10,000 with `max_candidates`). This avoids repeatedly collecting only the beginning of huge feeds.
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
  config/pscaper-site-nginx.conf public dashboard, read-only APIs and export gateway
  config/pscaper-cert-renew.*   project-scoped TLS renewal service/timer
  site/index.html               static dashboard served by the Go process
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
- `/etc/nginx/sites-enabled/pscaper-site` -> `/home/ubuntu/pscaper/config/pscaper-site-nginx.conf`
- `/etc/systemd/system/pscaper-cert-renew.{service,timer}` -> project-local TLS unit files

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

## Public dashboard, API and HTTPS

The production website is https://proxy.syndrid.com/ and is backed by the same
Go process on loopback via a **separate Nginx hostname**. The static dashboard
resides in `site/index.html`; public endpoints exposed via Nginx are:

- `GET /` — dark, responsive live dashboard (sanitized, no raw proxies)
- `GET /api/live` — counts, worker health, protocol coverage without proxy or source identities
- `GET /api/events?after=0&limit=12` — bounded in-memory recent validator
  results, including the actual public proxy address, protocol, latency,
  current score, and confirmed new Gold qualification. `after` is the
  last returned event cursor. Events are live data, never fabricated.
- `GET /api/history?range=1h|24h|7d` — persisted, bounded telemetry history with 30-second disk samples and finer live one-hour samples; survives restart
- `GET /api/v1/proxies?tier=alive&protocol=all&format=json&limit=1000` —
  public read-only proxy API, accepts alive/gold, http/https/socks4/socks5,
  JSON or TXT, limited to 5,000 per request. Optional `min_score`,
  `max_latency_ms`, `fresh_minutes`, `as_country`, and `asn` filters
- `GET /api/sources` — best feeds ranked by currently working proxy contributions and observed catalog yield (without exposing raw source URLs)
- `GET /api/asn` — top ASN registration countries and networks for alive proxies
- `GET /pool/{alive|gold|historical}/{filename}.txt` — strictly allowlisted
  read-only export files for the GitHub Actions publisher
- `GET /pool/stats.json` — current exported machine telemetry

The service management API remains bound only to `127.0.0.1:8788`; privileged
harvest/export paths are **not** public. Nginx routes no other paths.

Let's Encrypt hostname certificate + private key live inside
`/home/ubuntu/pscaper/data/letsencrypt` (not committed). Project-local
`pscaper-cert-renew.timer` performs twice-daily renew checks and reloads
Nginx after a successful renewal. The HTTP-01 challenge uses the app's
`site/.well-known/acme-challenge/` route; it does not need access to
other webroot folders.

## Fast live dashboard and publishing recovery (October 2026)

The public dashboard is a desktop-fit, charcoal network monitor using lightweight
vanilla CSS/SVG/JavaScript (no framework or extra browser downloads). At standard
desktop widths and a viewport height of 660px or greater, the entire screen is
displayed without scrolling at 100% zoom. Smaller/mobile viewports remain scrollable
so information is not clipped. Motion honors `prefers-reduced-motion`.

- `GET /api/live` exposes cumulative `newly_alive`, `lost_alive`, and
  `newly_discovered` scanner events (reset on app restart). Browser computes
  independent green new/recovered and red lost proxy deltas every 5 seconds.
- Live values are cached server-side for four seconds to avoid an expensive
  per-visitor scan of the historic records on the two-core VPS.
- Client-side SVG trends update every five seconds. A small project-local
  JSONL store keeps a rolling week of 30-second snapshots across restarts.
  The chart offers 1H, 24H, and 7D tabs without expanding the desktop layout.
- A full app restart resets session-only validation event counters,
  but saved chart history, proxies, and historical exports persist.

### GitHub data publishing — 30-second VPS schedule

The **active** VPS publisher uses a repository-scoped SSH deploy key with
write access only to `cmahmud/proxies`. Its dedicated systemd timer runs
at seconds 00 and 30 every minute. Each run fetches the latest data-repo
state and compares actual proxy lists; it skips the commit if those lists
are unchanged. The SSH deploy key, staging checkout and exports stay under
`/home/ubuntu/pscaper/`. No personal GitHub token is needed.

The application also refreshes its local exports every 30 seconds
(`PSCAPER_EXPORT_INTERVAL=30s`). The website still polls every 5 seconds.

```bash
systemctl status pscaper-publish.timer
systemctl list-timers pscaper-publish.timer
journalctl -u pscaper-publish.service -n 30 --no-pager
```

The workflow at
https://github.com/cmahmud/proxies/actions/workflows/pscaper-sync.yml
is **manual fallback only**; its scheduled trigger was disabled to avoid
competing writers. The Go application's built-in Git push is disabled;
only the isolated publisher writes to the proxy-data repository.

Fast publishing generates large repository histories if proxy lists keep
changing. Monitor repository size and API/push errors periodically.

## October 2026 quality and reliability upgrades

- Each source's recently observed working-proxy yield is measured and given a
  smoothed score. After enough observations, successful feeds refresh sooner
  and low-yield feeds refresh less often. Source health is shown as estimates,
  not guaranteed success attribution.
- The recheck scheduler prioritizes currently alive proxies and never-checked
  candidates, and backs off repeatedly failing, stale entries up to 24 hours.
- Scoring blends historical and recent outcomes, tracked over up to 16 checks.
  Gold additionally requires a recent check and (when enough data exists)
  at least four successes in the last five attempts. Expect Gold counts to
  fluctuate while old records receive fresh validation.
- Probe callbacks use an independently generated `PSCAPER_PROBE_SIGNING_KEY`
  and per-attempt challenge nonce. HMAC-SHA256 ensures untrusted proxies
  cannot forge the application's callback result merely by copying a JSON
  response. The signing key is never put in the proxy request or committed.
- Average latency and number of recently checked alive endpoints are
  available through the sanitized `/api/live` endpoint.
- Every 30 seconds, the GitHub publisher saves last-success, attempt,
  commit SHA, and health status under `data/publisher/status.json`. The
  dashboard highlights missing/stale publishing.
- The Git data checkout grew to 4.2 GB in a few days. It was replaced with
  a verified new shallow sparse checkout. The daily
  `pscaper-maintain.timer` now performs the same exclusive safe refresh
  inside `/home/ubuntu/pscaper/data/publisher/`. The source repo and
  application state are not touched.

Use `systemctl list-timers pscaper-maintain.timer` and
`journalctl -u pscaper-maintain.service -n 30` to inspect maintenance.

## Offline IPv4 ASN and country data

The project includes optional IP-to-ASN metadata supplied by
[IPtoASN](https://iptoasn.com/) using its downloadable public-domain
(PDDL 1.0) IPv4 unsigned-integer TSV database.

- The gzip database is stored at
  `data/intel/ip2asn-v4-u32.tsv.gz`, under the project tree and excluded
  from Git. IPs are looked up locally by binary search; no per-proxy
  requests are sent to a third party.
- `pscaper-intel.timer` downloads and verifies a refreshed dataset daily.
  The running Go process checks for refreshed data hourly.
- `GET /api/asn` returns a cached distribution of **AS registration
  countries** and autonomous systems among the currently alive list.
- `GET /api/v1/proxies?as_country=US&asn=13335&limit=100` can filter
  by AS registration country/number. API results also include
  `asn`, `as_country`, and `as_description`.
- The website offers an **AS GEO** compact tab, in addition to protocol
  coverage and source quality. These countries reflect network/ASN
  registration and **must not** be called precise physical proxy locations.

The IPv4 feed does not label IPv6 addresses. GeoIP accuracy, exit location,
anonymity tier, and working against arbitrary external destinations cannot
be established from ASN data alone. Those require separate controlled
measurements or an appropriately licensed location database.

## V2 compact one-screen dashboard, proxy terminal, and rollback

The original desktop layout was preserved byte-for-byte before the V2 update
under `site/backups/v1-original-20261016.html` (SHA-256:
`e1afdf31795f5998f191972649d11f6439abdcaa108436a63d7761b265c25084`).
To revert **only the website**, run:

```bash
cd /home/ubuntu/pscaper
./scripts/rollback-dashboard.sh
```

The October 16 V2 terminal layout is separately backed up as
`site/backups/v2-before-flow-20261016.html`. To restore the original
dashboard run `./scripts/rollback-dashboard.sh v1`; to restore the V2
dashboard **before** the smoother-flow enhancement run
`./scripts/rollback-dashboard.sh v2`. The no-argument script defaults to V1.

The current terminal polls its verified event API about every 1.8 seconds
and animates **one real event every 480 ms** instead of inserting a burst of
rows. A short bounded queue favors recent events; stale results are skipped
rather than replaying minutes of proxy history. FLIP/Web Animations moves
existing rows smoothly as one enters, while Gold-transition shimmer remains
event-driven. Pause and Clear work without adding scrolling, and reduced
motion is respected. These are live preview events, not a guarantee that
every single validation event is visible.

The rollback script first preserves the current dashboard in ignored
`recovery/ui-rollbacks/`, then copies the known-good original into
`site/index.html`. The Go app serves this file for each request, so no
restart or backend/publishing downtime is necessary. The backup is committed
to the application repository, independent of the VPS runtime.

V2 keeps the four primary metrics and the large chart in the original
charcoal theme. Below the chart is a low-noise terminal showing **real
validator results** every ~2.2 seconds using `/api/events`. Rows include
IP:port, protocol, most recent latency, score, and verified status;
qualified Gold rows stay muted gold, while **newly qualified Gold** gets
one short gold shimmer. Pause, Clear, and copy-address interactions are
local to the browser. This stream has a 512-event server-side buffer,
up to 40 results per request; it never invents proxy records.

The responsive desktop layout fits one browser viewport without a scrollbar
at standard desktop height and 100% zoom. Narrow/mobile layouts may scroll
rather than truncating the interface. Animations respect reduced-motion
settings.

The source configuration includes eight additional validated-format
URLs, after removing candidate URLs that returned 404/502 and suppressing
mirrored publisher duplicates. Being a healthy *feed* means it provides
parseable addresses; only the pscaper callback validator can classify
proxies as Alive or Gold. New feeds run with refresh intervals of
10–30 minutes rather than hammering publishers.

## Curated source expansion and ongoing discovery

The October 16 expansion increased the configured feed list from **126 to 329**.
A bounded offline audit examined **609 new candidate URLs** referenced by two
publicly maintained proxy-source indexes: maximilianfeix/proxy-scraper and
gfpcom/free-proxy-list. **332** returned at least six parseable public
IP:port addresses. Only **203 URLs** were promoted, representing **83 newly
configured repository/domain identifiers**, after publisher grouping, URL and
sample-content deduplication and an existing-source check.

New feeds have tighter limits than legacy feeds: **at most 100 candidates per
fetch, an 8 MiB response cap, and a one-hour base refresh interval**. The
engine's existing adaptive source yield ranking can update refresh behavior
after sufficient observations. All feeds are re-validated independently
against pscaper's own signed callback endpoints. Extra feed URLs do not equal
extra working proxies; mirrors, stale lists and overlapping publishers are
common. Scaling to 1,000 *distinct productive publishers* will require
additional discovery and format support, not just 1,000 file links.

The original 126-entry configuration was copied before promotion to
`recovery/source-backups/sources-before-extended-*.json` on the VPS.
The manually operated pipeline is:

```bash
cd /home/ubuntu/pscaper
python3 scripts/research-sources.py       # bounded audit; writes recovery/ report
python3 scripts/promote-sources.py        # dry-run: preview safe additions
python3 scripts/promote-sources.py --apply # backs up current config first
```

The weekly `pscaper-source-research.timer` reruns the audit and updates the
local report, but **does not automatically promote unknown hosts**.
New sources are picked up when the running collector enters its next harvest
cycle; a restart is not required. Some feeds may later become unavailable,
so continue to evaluate novel *working* proxies, error rates, overlap and
validation throughput instead of optimizing the feed-count number alone.

## Source-quality workflow

`python3 scripts/audit-sources.py` performs a bounded parallel download of the feed URLs and reports HTTP status plus parseable public IPv4/port samples under `recovery/feed-audit.json`. This is a **format and availability audit**, not a claim that the proxies are working. Only the app's callback validator determines the live pool. Data sources change or disappear; re-run the audit when pruning or expanding sources.

## Historical exports

- `exports/alive/`: currently passing proxies.
- `exports/gold/`: repeatedly verified, higher-scoring proxies.
- `exports/historical/all-discovered.txt`: every previously discovered proxy.
- `exports/historical/ever-alive.txt`: proxy has passed at least once.
- `exports/historical/ever-gold.txt`: proxy has qualified as Gold at least once.
- `exports/stats.json`: last export's point-in-time counters.

The dedicated VPS publisher is active at 30-second intervals. The Go engine built-in Git push remains disabled; never point proxy-data publishing at the application source repository.

## Safety and limitations

Free public proxies are untrusted, unstable and may intercept unencrypted traffic. **Never route authentication, personal or financial data through them.** The engine only reads configured public feeds (no internet-wide port scans). A working-proxy count is time-sensitive. Gold requires repeat validation, so it can initially be zero even if some Alive proxies have passed.

Previous source revisions and the original v1.3.0 Git bundle remain backed up locally in `recovery/`.

To protect live validation after the initial expansion revealed a burst of
~18,700 newly discovered endpoints and a nearly full 15,000-item queue,
the new 203 feeds were tuned from 250 candidates/2 hours to **100
candidates/hour**. This lowers burst pressure while sampling source files
more frequently; real working-proxy yield remains the success metric.
