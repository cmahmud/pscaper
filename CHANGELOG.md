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
