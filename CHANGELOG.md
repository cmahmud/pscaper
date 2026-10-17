## October 2026 — v1.5.2 gradual 10k feed registry scaling

- Audited 46 candidate text sources and tested actual inventory overlap,
  rejecting empty, duplicate, and already-covered publisher lists.
- Added five evidence-backed sources, raising the real configured
  registry from 329 to 334 (not counting CDN fallback mirrors).
- Added configurable, bounded per-harvest source admission, rotating
  oldest-due scheduling, a protected productive-source slice, and
  atomic fetch-timestamp persistence across restarts.
- Fixed handling of URI-prefixed proxy lines with trailing translated
  text/metadata, confirmed against the five real newly admitted feeds.
- Constrained source HTTP redirects to the originating publisher over
  HTTPS; SSRF/redirect and scaling fairness tests cover these rules.
- Expanded existing weekly read-only source index research, preserving
  manual review/promotion of additional candidate URLs.
- Preserved the previous binary/source registry, documented safe
  rollback and retained all live probe/Gold criteria.

## October 2026 — v1.5.1 intermittent feed repair and source confidence

- Added explicitly configured, same-repository HTTPS jsDelivr fallbacks for
  the four fyvri protocol lists. A transient GitHub raw 404/timeout now
  triggers a bounded alternate fetch instead of treating the feed as dead.
- Hardened fallback host/repository validation and redirect behavior.
  Healthy primary feeds are still fetched first; candidate checks remain
  independently validated and source count stays at 329.
- Added `novel_alive` and `novel_survived_15m` to per-feed data to distinguish
  verified contributions from copied listings and past successes.
- Source cadence promotion now also requires four unique currently Alive
  contributions and three demonstrated 15-minute survivors; this is
  conservative and does not disable feeds or change Gold qualification.
- Added unit tests for fallback recovery and attribution, a read-only
  24-hour comparison script, and a full v1.5.0 rollback.

## October 2026 — v1.5.0 real Proxy Quality Intelligence

- Persistent activation cohorts measuring actual signed-validation survival
  at 5/15/60 minutes; missing data stays pending and minimum sample sizes
  prevent premature quality percentages.
- Bounded read-only API for observed survival, unique IP/network/ASN
  diversity, and evidence-gated source refresh classifications.
- Source pacing requires stronger proof of newly validated unique proxies;
  no source deletion, Gold-rule changes, or aggressive worker scaling.
- Added nested QUALITY / ACTIVITY inside the existing INTEL panel; preserved
  minimal one-screen desktop layout and animated real proxy terminal.
- Added tests for cohort boundaries, observed failures, journal persistence,
  thread-safe snapshots, source confidence, and API semantics; rollback and
  release documentation are included.

## October 2026 — API release smoke checks

- Added a repeatable read-only API compatibility, pagination and status test.

## October 2026 — v1.4.2 initial state and responsive dashboard

- Initialize source-status inventory from all configured feeds, preserving
  persisted verified-yield metrics, before the first live harvest.
- Keep the four Coverage tabs within their single-screen panel at narrower
  desktop widths, without adding new content or additional scrollbars.
- Add test for correct immediate feed reporting on startup.

## October 2026 — v1.4.1 source reliability and sustained observation

- Exclude common special-purpose, non-public IPv4/IPv6 address blocks at
  the candidate parsing boundary; added explicit regression coverage.
- Add a weekly read-only Cloudflare edge IP integrity check, without
  automatic Nginx reloads or trust-list mutation.
- Add a bounded one-hour CPU/RAM/queue/pool/publishing observation script.
- Preserve the verified v1.4.0 binary for immediate parser-only rollback.

## October 2026 — v1.4.0 API and frontend intelligence

- Added a cached, sorted Alive-only API index shared by v1 and v2 to avoid
  full-database clone and sort on every read-only proxy request.
- Added read-only v2 API: strict filter validation, snapshot-bound cursor
  pages, conditional ETag/304, compact health, and reliability information.
- Added measured rolling validation throughput/success percentage and
  recoveries API; compact INTEL tab inside the existing Coverage panel.
- Added accessible chart-hover sample labels without altering the one-screen
  desktop layout, Gold animation, or streaming real-proxy terminal.
- Corrected site Nginx trusted Cloudflare real-IP configuration with
  official edge ranges, separated per-client rate limits and compression.
- Added API tests, versioned original-dashboard backup, full rollback
  script, and a public API v2 developer guide.

## October 2026 — v1.3.2 pool health and recovery

- Prioritize controlled follow-up checks for recently verified proxies that
  subsequently failed, without counting them Alive before validation passes.
- Preserve normal exponential backoff for persistently dead endpoints and
  never fast-track authentication/signature failures.
- Correct the scanner's session attempt count and source-yield presentation.
- Show one-hour Alive trends separately from system uptime in the one-screen
  charcoal dashboard, preserving original animations and terminal feed.
- Fix the reverse ordering of persisted one-hour chart history across service restarts.
- Share a four-second aggregate cache across stats, sources and dashboard
  requests to reduce repeated full database scans.
- Add tests, verified version bump, new dashboard backup and unified rollback.

## October 2026 — validation throughput and quality optimization

- Separate reserved high-priority queue for known-live proxy rechecks;
  discovery jobs cannot crowd them out, and workers retain fairness.
- Confirm transient failures with a second, longer signed challenge before
  demoting a previously Alive proxy; do not retry forged signatures.
- Bounded recheck-sweep memory, two-minute recheck scheduler, and an
  explicit per-harvest discovery queue limit.
- Fixed actual new-candidate attribution to source names and recorded
  persistent origin-source evidence of first successful verification.
- Adaptive feed priorities use verified novel proxy yield rather than
  duplicated or merely parsed address counts.
- Removed the validation-drain dependency in continuous mode. The VPS is
  already configured with non-continuous mode, which uses independent
  harvesting and 30-second local export timers; that setting is preserved.
- Added priority/confirmation/freshness metrics and integration/race tests.
- Preserved pre-optimization release and added a one-command engine rollback.

## October 2026 — source-load safety adjustment

- The first 329-feed harvest discovered roughly 18,700 new candidates and
  briefly filled the validation queue to near its configured capacity.
- Tuned the newly added 203 feeds to 100 sampled candidates per one-hour
  refresh rather than 250 per two-hour refresh, smoothing admission without
  increasing the scraper's workers or CPU budget.
- Maintained a project-local configuration backup before applying changes.

## October 2026 — source expansion and smooth terminal streaming

- Audited 609 previously unconfigured URLs from curated source indexes:
  332 yielded parseable candidate lists, 203 distinct healthy URLs were
  promoted from 83 newly represented repository/domain identifiers.
- Expanded configured feeds from 126 to 329, with strict 100-candidate,
  8-MiB and one-hour refresh limits for new sources to protect the VPS.
- Added reproducible, read-only research reporting and controlled manual
  promotion, plus a weekly audit timer without automatic untrusted promotion.
- Smoothed the V2 live terminal to render one verified result every 480 ms
  instead of inserting a batch all at once; added bounded queuing, smooth
  existing-row movement and preserved new-Gold animation.
- Committed a second rollback backup for the pre-flow V2 layout; rollback
  script now supports v1 (original) and v2 (before smooth streaming).

## October 2026 — V2 minimal live terminal and expanded feeds

- Added eight independently sampled candidate feed URLs from Proxmint,
  Relayglass and ProxyScraper (118 → 126); rejected failed and mirror URLs.
- Added bounded in-memory streaming validation events and public, allowlisted
  read-only `/api/events`, with real proxy address/protocol/score/latency and
  newly qualified Gold transitions.
- Preserved original website as a byte-exact tracked HTML backup and shipped
  `scripts/rollback-dashboard.sh` for rapid static-only restoration.
- Retained original charcoal dashboard, moved to a single-screen 2×2 panel
  layout, and introduced a compact authentic proxy terminal with optional
  pause/clear/copy and motion-sensitive Gold shimmer.
- Kept Go validation performance limits, API protections, separate data
  repository, and 30-second publishing unchanged.

## October 2026 — offline ASN enrichment

- Added a compact project-local IPtoASN IPv4 dataset, efficient Go lookup,
  and a verified daily update timer.
- Exposed ASN registration country/number/description on proxy API results,
  added ASN filters, public aggregate statistics and an AS GEO dashboard tab.
- Retained strict separation between ASN country and actual proxy geolocation;
  no per-proxy API calls or credentials required.

## October 2026 — collector intelligence and dashboard 2.0

- Reclaimed over 4 GB of loose Git objects by a verified checkout refresh,
  added a successful systemd-tested daily maintenance job using the publisher lock.
- Added HMAC-signed per-validation challenges, separate secret and replay checks.
- Added reservoir sampling throughout bounded source content and adaptive
  source yield/refresh metrics, with public top-feed summary.
- Prioritized reliable/recent proxies and introduced dead-endpoint backoff;
  implemented recent reliability scoring and stricter fresh Gold.
- Persisted one week of local aggregate telemetry with 1H/24H/7D ranges.
- Added last GitHub sync health, range/source tabs, average validation latency,
  and filterable read-only proxy API to the single-screen dashboard.
- Preserved all project data and service setup under /home/ubuntu/pscaper.

## October 2026 — 30-second publishing

- Local export snapshots refreshed every 30 seconds.
- VPS systemd publisher timer fires every 30 seconds.
- Actual proxy-list changes are required before creating a Git commit,
  preventing timestamp-only churn.
- Verified repository-scoped SSH deploy key; GitHub Actions remains a
  manual fallback rather than a competing periodic publisher.

## October 2026 — compact live monitor and five-minute publishing

- Redesigned dashboard as a charcoal, dense one-screen monitor on standard desktops:
  slim heading, four live metrics, SVG trend, protocol bars and scanner health.
- Added subtle chromatic animation, smooth value transitions, green/red event
  badges, live 5-second polling and reduced-motion accessibility.
- Exposed accurate cumulative validation transitions and bounded the cost of
  public telemetry with a four-second shared cache.
- Raised GitHub Actions publishing cadence to five minutes and documented how
  to verify actual scheduled runs.
- Prepared an independent five-minute VPS publisher using a
  `cmahmud/proxies`-scoped SSH deploy key; requires the repo owner to authorize
  write access before enabling the timer.

## October 2026 — public dashboard and GitHub Actions recovery

- Recreated `proxy.syndrid.com` live dashboard under `site/index.html`, using a distinct Nginx virtual host without changing the existing sites.
- Added sanitized public telemetry and a bounded, read-only `/api/v1/proxies` API; no admin endpoints are published.
- Served allowlisted /pool exports for the separate `cmahmud/proxies` GitHub Actions publisher.
- Issued a Let's Encrypt certificate under the project directory and added a renewable systemd timer.
- Replaced server-side Git pushing with scheduled GitHub Actions syncing every 30 minutes, using scoped GitHub-managed authentication.

## October 2026 — pscaper VPS recovery and deployment

- Migrated restored Go application from prior project name to pscaper.
- Replaced 19 unavailable/empty feeds and added 31 independently sampled, working feeds (118 configured).
- Capped per-feed candidate admission to 2,000 by default to protect the 2-core VPS.
- Restored historical `all-discovered.txt` import compatibility.
- Avoided journal churn on unchanged duplicate candidates.
- Optimized historical TXT serialization with linear-time `strings.Join`.
- Added bounded feed auditing and validation tests for gateway forwarding.
- Routed public callbacks through the existing 80/443 Nginx ingress, leaving management loopback-only.
- Added self-contained systemd/Nginx configurations under `/home/ubuntu/pscaper/config/`.
- Kept runtime data, exports, credentials, certificates and build artifacts outside Git tracking.

# Changelog

## 1.3.1 — 2026-08-22

- Added supported public GitHub export repositories for operators who intentionally publish their validated pool.
- Fixed GitHub profile attribution for future automated export commits by using the authenticated account's ID-based GitHub `noreply` author address by default.
- Added explicit `PSCAPER_GITHUB_VISIBILITY`, `PSCAPER_GIT_AUTHOR_NAME`, and `PSCAPER_GIT_AUTHOR_EMAIL` controls.
- Made GitHub setup migration-safe when moving to a new VPS by attaching the fresh export working tree to an existing `main` history before the first push.
- Fresh VPS migrations now restore discovered/EverAlive/EverGold state from `historical/` exports when the old runtime snapshot is unavailable.
- Changed fresh-install defaults to a conservative 600 workers / 100,000 queue / 8 source fetches for the permanent 2-OCPU, 12-GB ARM host.
- Kept the management API loopback-only; public distribution remains via the export repository rather than exposing control endpoints.

## 1.3.0 — 2026-08-18

- Expanded source catalog to 106 protocol feeds across 31 public-list publishers.
- Added per-source refresh cadences and large-feed byte limits.
- Preserved continuous harvest → validate → export/push cycling without hammering feeds that are not due.
- Raised fresh-install source concurrency to 24 and queue capacity to 3,000,000.
- Added source scheduling telemetry to the TUI/API.
- Preserved v1.2 historical state: discovered, ever-alive, ever-gold, and periodic archive snapshots.
- Preserved current alive/gold exports and changed-only Git pushes.
- Added source cadence/max-size regression coverage.

## 1.2.0

- Added persistent EverAlive/EverGold history and PeakScore.
- Added historical append-only exports and periodic current-pool archives.
- Split current vs historical counters in the TUI.

## 1.1.0

- Added continuous cycle mode: harvest → validate queued work → export/push → repeat.
- Added separate alive/dead recheck cadences and cycle telemetry.
