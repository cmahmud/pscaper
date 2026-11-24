# pscaper — Codebase Improvement Roadmap

Review date: 2026-11-23 · Reviewed commit: `3e033ef` (v1.5.3)

Scope: the Go engine (`cmd/`, `internal/app/`), the operational scripts (`scripts/`),
the systemd units and Nginx configs (`config/`), the 334-feed registry, and the
dashboard (`site/`).

## How this review was done

- Every Go source and test file was read, together with the operational scripts,
  systemd units, Nginx configs and docs.
- `go vet ./...` is clean and `go test -race ./...` passes (Go 1.24.9). The module also
  compiles with `-lang=go1.22`, so the `go 1.22.0` directive is valid.
- Suspected bugs were checked with throwaway tests against the real package; those
  files were deleted afterwards and the working tree is clean. Confidence tags:
  **[verified]** reproduced by execution · **[verified-by-reading]** traced in code ·
  **[code]** reasoned from source · **[ops]** from configuration or scripts not run here.

## Executive summary

The design is solid: HMAC-signed probe responses, SSRF-aware feed fetching with an
allow-list of redirect targets, reservoir sampling over bounded feeds, a journaled
store with streamed compaction, a cursor-paginated v2 API, and a dashboard served by
Nginx separately from the backend. The problems below are correctness and consistency
issues, not architectural ones. The most serious:

1. **`https` proxies can never be confirmed** via a working path, and `https://` feed
   lines are validated as plain HTTP (**H1**).
2. **Source-quality statistics that v1.5 depends on silently degrade**: a re-import
   bug dilutes yield forever (**H2**, verified), and the fetch counter the priority
   policy requires is never persisted, so the policy never engages (**H4**, verified).
3. **The journaled store is not a single writer**: `Close` races `Compact`
   (**H3**), nothing prevents two processes on one directory (**H2**), and a
   compaction failure silently discards the dirty set (**H6**).
4. **A clean first start registers zero sources**, and `origin_source` is not restored
   from history (**H5**).

| ID  | Title | Priority |
|-----|-------|----------|
| H1  | `https` proxies cannot be validated as HTTPS; `https://` feed URIs degrade to `http` | P0 |
| H2  | Historical re-import inflates `NovelSeen` and dilutes source yield; no single-instance lock | P0 |
| H3  | `Store.Close` races the compaction loop; compaction failures leak or lock out writes | P0 |
| H4  | Per-source `Fetches` is memory-only, so the priority reservation never engages | P1 |
| H5  | A clean first start registers zero sources; `origin_source` is lost on history import | P1 |
| H6  | Test coverage misses the paths that hide these bugs (HTTP filter validation, restart, multi-process) | P1 |
| H7  | Two publishers with different paths, histories and failure modes; optional operators are not installed | P1 |
| H8  | `README.md`/`docs/API_V2.md` describe v1.4.2; `smoke-api-v2.py` asserts 329 feeds; no CI | P1 |
| H9  | Health signals mislead: `source_errors` counts every feed that ever failed, watchdog blind to stalls | P2 |
| H10 | `handleASN` scans the whole store under the engine read lock every 90 s | P2 |
| H11 | Event ring buffer shifts up to 512 entries on every add; history log records false zeros | P2 |
| H12 | Dead code and config; hard-coded host paths; two divergent default sets | P3 |

---

## 1. Bugs and consistency defects

### H1. `https` proxies cannot be validated as HTTPS; `https://` feed URIs degrade to `http` — P0 [verified-by-reading]

**Where:** `internal/app/validator.go:38-113`, `internal/app/sources.go:270-283`.

`Validate()` calls `httpProxy(ctx, p, true)` only for `p.Protocol == "https"`, and
`httpProxy()` builds its proxy-scheme list from the same field:

```go
proxySchemes := []string{"http"}
if p.Protocol == "https" {
    proxySchemes = append(proxySchemes, "https")
}
```

For an `https` record, the first attempt dials the proxy with the plain `http` proxy
scheme but targets the TLS listener on `ProbeHTTPSPort`. A plain conversation to a TLS
port fails. The only remaining attempt uses Go's `https` proxy scheme, which means
"CONNECT over TLS to the proxy", a rare dialect. The plaintext conversation to the TLS
port that these feeds publish is never attempted, so a working HTTPS proxy is recorded
as dead forever. Meanwhile `http` records are only tested against the plain HTTP probe,
so HTTPS forwarding is never measured for them either.

`parseCandidate()` prefers the URI scheme over the feed hint (`if p == "" { p = proto }`
only applies to unknown schemes). A line like `https://1.2.3.4:8080` in an
`http`-hinted feed therefore becomes an `https` record and inherits the broken path.

**Recommendation.** Probe HTTPS for every protocol: try the plaintext conversation to
the TLS probe port first, then the TLS-to-proxy dialect. Represent "HTTP-capable" and
"HTTPS-forwarding verified" as two facts on the record rather than one protocol label.
Add a test with a fake forwarding proxy in front of the TLS probe.

### H2. Historical re-import inflates `NovelSeen`; no single-instance lock — P0 [verified]

**Where:** `internal/app/engine.go:71-128`, `internal/app/engine.go:463-490`,
`internal/app/store.go:217-277`, `internal/app/store.go:28-45`.

(a) **Yield dilution.** `NewEngine` always runs `ImportHistoricalFiles`, which unions
`historical/*.txt` into the store and marks ever-alive records; it then rebuilds each
source's `NovelSeen`/`NovelConfirmed` by walking `OriginSource`. On the next harvest,
`mergeSourceStatuses` adds `NewCandidates` on top of a `NovelSeen` that already counts
the same records. With 5 confirmed of 6, one harvest of 6, and `Stats()`, the yield
falls from 83.3 % to 41.7 %; every later restart and harvest compounds it, and the
value that `sourceConfidence()` and the dashboard rely on drifts to zero.

(b) **Two processes on one directory.** Nothing locks the journal; `OpenStore` opens
with `O_APPEND` and the systemd unit has no `flock`. A second instance (a manual
`./bin/pscaper serve` beside the service) will interleave journal appends against the
live process's compaction and both will write `*.tmp` snapshots.

**Recommendation.** Derive `NovelSeen` from the store (a cached per-origin count) rather
than accumulating, or persist an identity set per source. Add a restart-stability test.
Take an exclusive `flock` on `data/pscaper.lock` in `OpenStore` and exit if it is held.

### H3. `Store.Close` races the compaction loop; a compaction failure leaks or locks out writes — P0 [verified-by-reading]

**Where:** `internal/app/store.go:110-117`, `internal/app/store.go:356-428`,
`internal/app/engine.go:686-699`, `cmd/pscaper/main.go:52`.

`Compact()` uses `ioMu` for the file surgery but not for the whole operation, and
`Engine.Run` returns as soon as the context is cancelled. `main` then calls
`defer store.Close()`, whose body is `Flush(); Compact(); journal.Close()` with no
lock. If the 30-minute `compactLoop` is in the middle of a long, streamed compaction
(or starts concurrently with close), both can rename over the snapshot and reopen their
own closed journal handle.

Separately, if `Compact()` fails after the rename (e.g. reopening the journal fails,
`store.go:420-427`), it returns with `s.journal == nil` or closed. Every later `Flush`
returns an error, and the next successful `Compact` begins with `Flush` and returns
early, so **all subsequent writes are lost until restart**, silently. The same effect
applies while the compaction loop is running: `Flush` and `Compact` both block on
`ioMu` and the dirty set keeps growing.

**Recommendation.** Signal goroutines to stop and wait for them (`sync.WaitGroup`)
before `Close`. Hold `ioMu` for the whole `Close`. And/Or have a single writer
goroutine own the journal handle. Return a permanent error state if compaction leaves
the journal unusable, and surface it as `degraded` in `/health`.

### H4. Per-source `Fetches` is memory-only, so the priority reservation never engages — P1 [verified]

**Where:** `internal/app/engine.go:103-133`, `internal/app/engine.go:463-490`,
`internal/app/source_schedule.go:30-73`, `internal/app/quality.go:147-160`.

`Fetches` is only incremented inside `mergeSourceStatuses`, and `NewEngine` rebuilds
`e.sources` without restoring it. After any restart every source reads `Fetches = 0`
and `sourceConfidence` returns `"learning"` until five more fetches land. In
`selectSourceBatch` the reserved slice contains only `"prioritize"` sources, so over
budget the prioritised slice is empty and selection is purely oldest-due.

I reproduced this with `selectSourceBatch` and ten sources in the post-restart state:
with a budget of 8, **zero** prioritised sources were selected. The rule that reserves
up to a quarter of each batch for productive sources never engages in the state that
matters most.

**Recommendation:** persist `Fetches` next to `sourceLastFetch`, increment it per
completed fetch (even empty ones), and add a test that builds the post-restart state.

### H5. A clean first start registers zero sources; `origin_source` is lost on history import — P1 [verified-by-reading]

**Where:** `internal/app/engine.go:87-133`, `internal/app/store.go:243-260`.

`NewEngine` builds `e.sources` from records whose `OriginSource` is set and not
`"history"`. Records created by `ImportHistoricalFiles` are given `Sources:
[]string{"history"}` and no `OriginSource`, so if a migration imports the export repo
before its first harvest, `Total` is populated (via `store.Total` in `Stats`) but
`source_feeds` is 0 until the first harvest replaces the list. Existing records never
gain an `OriginSource` once imported, so their real publisher is lost from source
attribution permanently. `TestConfiguredFeedCountAvailableBeforeFirstHarvest` passes
only because its fixture builds records through `UpsertCandidate`.

**Recommendation.** Always seed `e.sources` from the configured registry, with zeroed
counters, before attribution. Keep `origin_source` when an imported record later arrives
from a feed.

---

## 2. Coverage and process

### H6. Test coverage misses the paths that hide these bugs — P1 [verified-by-reading]

**Where:** `internal/app/api_v2_test.go`, `internal/app/app_test.go`.

- `parseV2Filter`, `encodeV2Cursor`, `decodeV2Cursor` and `matchesV2Proxy` have no
  direct tests. Query validation is tested only through one handler-level call, so
  most of its error branches are unexercised.
- No test starts two engines over the same export and asserts yield stability (H2a),
  no test covers a restart followed by `selectSourceBatch` (H4), and no test exercises
  `OpenStore` twice on one directory (H2b).
- No test drains the `https` probe path with a fake forwarding proxy (H1).
- `.github/` does not exist, so nothing runs these tests on push.

**Recommendation.** Add a CI workflow (`go vet`, `go test -race`, `gofmt -l`, docs/feed
count consistency, Nginx route allow-list extraction) and the four tests above. The
existing suite is otherwise strong: signed-probe forgery, cursor ETags, compaction
under concurrent writers, and SSRF redirect rules are all covered.

### H7. Two publishers; optional operators not installed — P1 [code] [ops]

**Where:** `scripts/publish-to-github.sh`, `scripts/publish-to-github.py`,
`internal/app/engine.go:731-845`, `scripts/install.sh`.

- The in-process publisher (`ExportAndPush` → `gitPush`) runs `git add -A`, `commit`,
  `push` in `PSCAPER_EXPORT_DIR` every `ExportInterval`, with a 90 s timeout and no
  rebase or retry. If `PSCAPER_EXPORT_DIR` is ever the source checkout, it commits
  everything there.
- The shell publisher runs from `pscaper-publish.timer` every 30 seconds against
  `data/publisher/repository` (sparse `alive gold historical`), locks, retries once on
  a moved head, and writes `status.json` that `/api/live` reads.
- Both default to pushing `main`, with different validation (`total > 100000` only in
  the Python path) and different file sets (`archive/` is only in the Go path).
- `install.sh` installs `pscaper.service` and the probe Nginx site only. The watchdog,
  publisher, dashboard sync and memory-guard drop-in must be installed by hand, and
  `README` does not say so.

**Recommendation.** Pick one publisher. If the shell publisher is production, default
`PSCAPER_GIT_PUSH=false` and reduce the in-process path to writing files. Assert both
paths emit identical `alive/`, `gold/`, `historical/` trees. Have `install.sh` install
every unit and verify the timers are enabled.

### H8. Stale docs, stale smoke literal, no CI — P1 [verified-by-reading]

**Where:** `README.md:5-8`, `docs/API_V2.md:3`, `scripts/smoke-api-v2.py:20`,
`cmd/pscaper/main.go:15`.

`README.md` still says "current maintained application version: **1.4.2**" and
"**329 configured feed URLs**"; `docs/API_V2.md` still opens with "pscaper 1.4.2";
`smoke-api-v2.py` asserts `source_feeds == 329`. The code is v1.5.3 with 334 feeds, so
the documented release smoke test fails out of the box.

**Recommendation.** Generate the version and feed count from one source and check them
in CI; have the smoke test read `config/sources.json` for the expected count.

---

## 3. Performance and observability

### H9. Health signals mislead — P2 [code]

**Where:** `internal/app/engine.go:982-991`, `internal/app/api_v2.go:242-253`,
`scripts/pscaper-health-watch.sh:3-31`.

- `source_errors` counts every feed whose last parsed status still has an `Error`. A
  feed on a daily refresh stays counted for up to a day after one transient failure, so
  `/api/v2/health` reports `degraded` almost permanently and operators stop reading it.
- The watchdog only checks `GET /health` and restarts after three failures. It never
  reads `cycle_phase`/`last_cycle_end`, so the "alive but still not harvesting" state
  that motivated v1.5.3 is invisible to it.
- There is no `pprof` listener, so a stall can only be diagnosed by restart.

**Recommendation.** Report only *recent* failures, add a `stalled_for_seconds` field,
and have the watchdog restart if the cycle has not advanced for 3× `HarvestInterval`.
Add an opt-in loopback-only `PSCAPER_PPROF_ADDR`.

### H10. `handleASN` scans the whole store under the engine read lock — P2 [code]

**Where:** `internal/app/server.go:239-292`.

Every 90 s `/api/asn` takes `Store.mu.RLock` (`VisitRecords`) and iterates all records,
not just alive ones, while validators wait for the write lock. On a million-record store
this is a long pause every 90 s under traffic.

**Recommendation.** Maintain per-ASN counts when a record flips Alive (in
`ApplyValidation`), then make `/api/asn` read cached counters.

### H11. Event ring buffer churn and false-zero history — P2 [verified] [code]

**Where:** `internal/app/events.go:35-49`, `internal/app/history.go:13-73`,
`internal/app/server.go:148-181`.

- Every validation of an alive proxy calls `EventBuffer.Add`, which does
  `copy(b.events, b.events[1:])` once full: 511 struct copies under the write lock per
  call.
- `HistoryLog.Append` writes whenever ≥30 s elapsed, with whatever the sampled queue
  depth was. Idle samples of 0 are persisted, so the 1h/24h/7d trend on the dashboard
  shows troughs that are sampling artefacts, not load. The comment above the sampler
  promises a heartbeat that does not exist.

**Recommendation.** Use a head-indexed ring and drain in order on read. Persist history
on a fixed schedule and carry the last known depth forward, or label the field as a
sample.

### H12. Dead code, dead config, hard-coded paths, divergent defaults — P3 [verified-by-reading]

- `Config.CycleBackoffMin/Max` and `PSCAPER_CYCLE_BACKOFF_*` are validated but never
  read (`config.go:35-36, 70-71, 104-108`); `Engine.waitForDrain` (`engine.go:560`)
  and `Store.ensureDir` (`store.go:572`) are unused.
- `config.go` defaults (`Workers=3000`, `QueueSize=3,000,000`) differ sharply from
  `.env.example` (`120`, `15,000`). The large default reserves ~45 MB of channel memory
  at startup and leaves discovery admission open long after a 120-worker pool should
  back off.
- Paths like `data/publisher/status.json` and `data/telemetry/history.jsonl` are
  relative to the working directory rather than `cfg.DataDir`; scripts hard-code
  `/home/ubuntu/pscaper` and `/var/www/pscaper`.
- `smoke-api-v2.py:20` asserts a 329 feeds literal (H8).

**Recommendation.** Delete dead code, derive the queue default from workers and
timeout with a test that bounds it, route every path through `cfg.DataDir`, and move
host-specific values into `.env`.

---

## 4. Security review

**Existing controls are strong.** HMAC-signed probes with nonces, trusted-real-IP
restricted to loopback, redirects confined to the originating publisher (exact jsDelivr
fallback shape), special-purpose ranges rejected, a loopback-only management API behind
an Nginx allow-list with rate limits, and a publisher script that refuses `.env`,
certificates and control endpoints.

Gaps worth closing:

1. **Feed command execution and the dashboard share one binary.** The HTTP listener
   serves both the static dashboard and the POST action routes
   (`/api/action/harvest`, `/api/action/export`). They rely on Nginx never forwarding
   them. There is no unit test asserting Nginx's route list and the mux stay aligned;
   see H6. This is an operational control, not a code control.
2. **TLS on the HTTPS probe.** `InsecureSkipVerify: true` is correct for the ephemeral
   self-signed certificate (`validator.go:71, 364-378`), but the probe hostname is a
   public FQDN behind Nginx with its own certificate. Whether the proxy agent actually
   reaches Nginx's certificate (and therefore whether enabling verification against it
   would make forged probe responses observable) depends on deployment topology the
   repository does not document. Document it, and enable verification if Nginx's
   certificate is what proxies see.
3. **No dependency surface.** `go.mod` has zero third-party modules, which removes a
   whole class of risk. Keep it that way, and add `go list -m all` to CI.
4. **`.env` is `source`d** by `doctor.sh`, `setup-github.sh` and the upgrade scripts.
   In a shared host that is remote code execution as `ubuntu`. Use a strict parser or
   `EnvironmentFile=` only.
6. **The Cloudflare drift check silently truncates.** `scripts/check-cloudflare-realip.py:28`
   reads only the first 12,000 bytes of Cloudflare's page. A list that grows past
   that limit would produce a false "drift" or, worse, miss ranges after the cut-off.
   Read the full response and bound it at an explicit, checked maximum instead.
5. **`/api/live` reads `data/publisher/status.json` without a size cap**, and
   `/pool/*.txt` responses are unthrottled beyond Nginx. Both are low risk but cheap
   to bound.

---

## 5. Suggested order of work

1. **P0:** H1 (proxy capability), H2 (yield stability, instance lock), H3 (store
   lifecycle), plus the CI workflow and stale-literal fixes from H6/H8.
2. **P1:** H4 (persist `Fetches`), H5 (seed sources), H7 (single publisher, full
   installer), H9 (health semantics), the missing tests from H6.
3. **P2:** H10, H11, and the TLS probe certificate question.
4. **P3:** H12 cleanup.

## Limitations of this review

- Scripts and systemd units were read, not run; findings tagged [ops] should be
  confirmed on the live host.
- The behavioural probes (H1's path analysis, H2, H4, H11) used the real package
  through throwaway tests that were removed afterwards.
- The dashboard JavaScript was inspected for API usage and escaping, not reviewed for
  visual design or accessibility.
- `docs/` and `CHANGELOG.md` were spot-checked, not diffed exhaustively.
