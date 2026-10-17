# pscaper v1.5 — measured proxy quality

## Purpose

This release measures *observed* proxy survival at 5, 15 and 60 minutes,
distinguishes unique endpoints from IP/network/ASN diversity, and adjusts
source collection cadence only when verified source evidence is sufficient.

No existing Gold qualification rule, public proxy validation challenge,
worker count, GitHub publishing schedule, or VPS resource limit is changed.

## Cohort survival semantics

On a successful signed validation, an endpoint starts a new activation
cohort. Subsequent successful signed validation at/after a 5-, 15-, or
60-minute horizon counts as survival **only when no failed check has been
observed since activation**. An intervening failed validation resolves all
unresolved horizons as non-survival. Milestones not yet reached or without
a later check remain pending and are not counted as surviving.

This measures *observed validation continuity*, not a guarantee the proxy
was reachable at every second between checks, and is not a forecast of
future availability. The measures start with v1.5; historic Alive records
are not retroactively assigned survival credit.

Each proxy stores its own optional, compact cohort accumulator in the
existing snapshot/journal, so restarts retain the observations. Multiple
activation episodes from the same proxy can contribute multiple cohort
samples. The API does not present a percentage until that horizon has at
least 30 evaluated episodes; counts are available earlier to show progress.

## API

`GET https://proxy.syndrid.com/api/v2/quality`

JSON keys:

- `status`: `collecting` or `measured` (all three horizons have 30+ samples).
- `survival`: three cohort objects with `minutes`, `samples`,
  `survived`, and `rate_pct` (null before any observations).
- `diversity`: `endpoints`, `unique_ips`, `unique_networks`,
  `unique_asns`, and `asn_identified`. Network uniqueness uses
  IPv4 /24 prefixes and IPv6 /48 prefixes. ASN counts reflect *registration
  metadata*, not verified physical location.
- `source_decisions`: number of feeds in learning / normal / prioritize /
  deprioritize states.
- `sample_threshold`: 30.
- `gold_criteria_unchanged`: true.

The endpoint is read-only, rate-limited as telemetry, and reuses the
existing four-second stats aggregate plus an approximately 12-second
Alive-only diversity cache.

### Patch v1.5.1 — verified source contribution and feed failover

The per-source telemetry now reports `novel_alive` (originally found
by that source and currently Alive) and `novel_survived_15m` (originally
found there with an observed 15-minute survival success). Source
priority requires >=4 currently Alive novel contributions and >=3
15-minute survivors **in addition to** the existing sufficient-fetch,
verified-novel contribution and yield gates. This protects against
prioritizing a feed whose working endpoints have all disappeared.

Four GitHub raw source URLs for `fyvri/fresh-proxy-list` now include
equivalent HTTPS jsDelivr fallback URLs to handle intermittent GitHub
404 responses. Fallback requests are restricted to the exact same
repository, branch and path; the primary URL is still tried first.
No new proxy is marked Alive without signed validation.

## Source pacing

Productivity reflects *unique newly discovered proxies that later passed a
signed validation*, rather than repeated copies in different source files.

- Learning: fewer than 5 completed fetches or 300 novel endpoints.
- Priority: at least 8 verified novel proxies and at least 0.8%
  verified-novel yield, **after** meeting the learning threshold.
  Refresh interval may halve, but not below the existing limit.
- Deprioritized: 800+ novel endpoints, at most 2 verified and verified
  yield below 0.05%, after the fetch confidence gate. Refresh interval
  may double, bounded by the existing maximum.
- Otherwise the interval remains the configured base value.

No feed is deleted, permanently disabled, or made to publish unchecked
proxies. Statistics are reconstituted from persisted origin-source records
on restart. The weekly discovery audit still requires manual promotion.

## Website

The four existing Coverage tabs remain. In **INTEL**, the nested
**QUALITY / ACTIVITY** switch changes four compact metrics without
creating another panel or scroll area. QUALITY shows 5/15/60-minute
survival and distinct network count; ACTIVITY preserves the existing
rolling validation attempt and recovery view. Animated Gold proxy rows
and the fixed desktop layout remain unchanged.

## Rollback

Known-good v1.4.2 executable and the previous exact dashboard are at
`recovery/v15-20261017/`; the previous dashboard is also tracked at
`site/backups/v142-before-quality-20261017.html`.

```bash
cd /home/ubuntu/pscaper
./scripts/rollback-v15.sh
```

The rollback restores only pscaper's binary, dashboard, and its Nginx
configuration, preserving the existing proxy database and other VPS
services. The earlier binary simply ignores the new optional proxy
quality fields retained in historical records.
