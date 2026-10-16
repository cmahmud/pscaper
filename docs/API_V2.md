# pscaper Public API v2

pscaper 1.4.2 keeps the old public endpoints working while introducing a
smaller, faster developer interface. The API is a **read-only** view of
independently signed probe results, not a guarantee of availability to
third-party destinations.

Base URL: `https://proxy.syndrid.com`.

## Endpoints

- `GET /api/v2/proxies`: sorted, filtered, cursor-paginated **currently
  validated** proxy endpoints. Default tier `alive`, default limit 100
  (maximum 250). Gold must meet the engine's configured Gold threshold.
- `GET /api/v2/health`: public health with proxy pool and fetch failures,
  distinct from mere website availability.
- `GET /api/v2/intelligence`: measured validation attempts per minute,
  observed pass percentage, confirmed losses, and transient recoveries from
  actual rolling counter snapshots. Warm-up is reported explicitly.
- `GET /api/v1/proxies`: existing API remains backward compatible, retaining
  the old request parameters, result ordering, 5,000-result maximum and
  plain-text output option.
- `GET /api/live`, `GET /api/history`, `GET /api/sources`,
  `GET /api/events`, and `GET /api/asn` remain unchanged. The
  dashboard uses the Intelligence endpoint but otherwise keeps existing
  telemetry and live proxy flow.

## Examples

```bash
curl -s 'https://proxy.syndrid.com/api/v2/proxies?limit=20'

curl -s 'https://proxy.syndrid.com/api/v2/proxies?tier=gold&protocol=socks5&min_score=85&limit=50'

curl -s 'https://proxy.syndrid.com/api/v2/proxies?as_country=US&fresh_minutes=30&max_latency_ms=1500&limit=100'

curl -s 'https://proxy.syndrid.com/api/v2/intelligence'
```

`as_country` is the **registration country of the autonomous system**,
not guaranteed physical proxy location.

### Pagination

The JSON response has `generated_at`, `total`, `count`,
`limit`, `next_cursor`, and `proxies`. When `next_cursor` is not
null, request the next page using the **same filters and limit** with
`cursor=<opaque token>`. A cursor binds to its filter values and one
immutable sorted snapshot. Several recent snapshots are retained in memory;
a stale cursor returns **HTTP 409** with advice to restart pagination.
Cursors do not survive server restarts.

A page's `proxies` are ordered by descending score, ascending latency,
and deterministic URL. Each result contains protocol, host, port, score,
latency, last check/success, success/failure counters, recent check success
percentage, and ASN registration metadata. Addresses that have not passed
the validator are never returned.

### Filtering

| Parameter | Allowed values |
| --- | --- |
| `tier` | `alive` (default), `gold` |
| `protocol` | `http`, `https`, `socks4`, `socks5`, `all` |
| `limit` | integer 1–250, default 100 |
| `min_score` | finite number 0–100 |
| `max_latency_ms` | integer 0–600,000; 0 disables |
| `fresh_minutes` | integer 0–10,080; 0 disables |
| `as_country` | two-letter uppercase/lowercase ASN country |
| `asn` | positive unsigned 32-bit ASN |
| `cursor` | opaque next-page token |

Unknown, repeated, malformed or out-of-range filter parameters return
**HTTP 400**. Query filters are strict, unlike legacy v1 which accepts
some malformed numeric strings for compatibility.

### Caching

The application keeps a **sorted Alive-only index for approximately eight
seconds**. It scans the full record store only once per refresh rather than
cloning every historical dead proxy for each request. v1 and v2 share that
index, without changing stored validation records.

v2 responses include an ETag and `Cache-Control: public,
max-age=5, must-revalidate`. Send `If-None-Match` to receive HTTP 304
when the requested snapshot has not changed.

### Rate limiting and Cloudflare

Nginx trusts `CF-Connecting-IP` **only from Cloudflare's published
edge CIDRs**, in `config/cloudflare-realip.conf`. Other connections use
their true connecting IP; client-supplied spoofed headers are ignored.
Each visitor now has independent token buckets:

- Proxy API v1/v2: 30 requests/minute, burst 12
- Live event API: 120 requests/minute, burst 30
- Telemetry/intelligence API: 240 requests/minute, burst 30

Exceeded limits return **HTTP 429**. Cloudflare's IP ranges are a
versioned snapshot, obtained from the official
`https://www.cloudflare.com/ips-v4` and `/ips-v6` lists.
They need periodic review if Cloudflare changes its network range.
Do not trust all IPs or entire public address ranges.

Nginx also enables compression for JSON and text. **No admin action
endpoints are exposed publicly.**

## Interpretation and limits

An Alive result confirms a successful signed callback through the
reported proxy at its most recent check. It is not a claim of anonymity,
residential IP, future availability, geographic residence, or arbitrary
third-party reachability.

The Intelligence tab displays measurements after enough in-process samples
arrive and may show warming up just after a restart. Success percentages
refer to real validation **attempts**, including confirmation attempts,
not the fraction of newly discovered addresses that work.

## Rollback

The pre-upgrade application binary and Nginx configuration are preserved
under `recovery/api-v2-20261016/`. The v1.3.2 dashboard is tracked at
`site/backups/v132-before-intelligence-20261016.html`.

```bash
cd /home/ubuntu/pscaper
./scripts/rollback-api-v2.sh
```

Rollback touches only the pscaper executable, its dashboard and its own
Nginx site configuration. It does **not** remove proxy records, affect other
VPS services or revert the separately published proxy data.
