# pscaper — Codebase Improvement Roadmap

Audit of `cmahmud/pscaper` at v1.5.3 (`3e033ef`, 2026-10-25). Scope: the Go
backend in `internal/app` and `cmd/pscaper`, source harvesting and validation,
the v1/v2 HTTP API, the dashboard, performance and memory behaviour, security,
tests, and the operational layer under `scripts/`, `config/` and `site/`.

Every claim below is tagged with how it was confirmed:

- **[code]** verified by reading the named file and function.
- **[run]** verified by executing it: `go vet`, `go build`, `go test`, and
  `go test -race` against Go 1.23.12 all pass on the current tree.
- **[runtime]** cannot be settled from the repository; it depends on the live
  VPS, systemd version, or out-of-tree state. Treated as a hypothesis, not a bug.

## Executive summary

pscaper is a compact, dependency-free Go service (24 source files, ~6,000 lines,
`go.mod` declares no modules) that continuously harvests public proxy lists,
validates each candidate against a **signed callback probe**, and publishes a
quality-scored pool. The core loop is well built: validation is cryptographically
sound, redirects are constrained against SSRF, memory is bounded, and there is a
genuinely useful regression suite (52 tests) that passes under the race detector.

The highest-value work is **not** in the engine. It is in four areas:

1. **Documentation drift.** README, CHANGELOG and API docs disagree with the code
   on version numbers, feed counts, probe certificate handling, survival-cohort
   semantics and source pacing. An operator following the README will hit wrong
   assumptions.
2. **A handful of real correctness gaps** in cohort accounting, HTTP-proxy scheme
   separation, and operational scripts (registry size asserted in a smoke test,
   hard-coded feed counts).
3. **Test coverage shape.** The scheduler, quality and API layers are well tested;
   the validator's SOCKS wire formats, the whole `StartAPIServer` route table, and
   the TUI have no direct tests, and there is no CI workflow in the repository.
4. **Operational fragility.** Publishing, rollback and watchdog scripts carry
   non-atomic file swaps and restart loops that can wedge publishing until a human
   intervenes.

No correctness issue found here is a live data-loss or security hole at the
current defaults. The most urgent items are cheap fixes that prevent future
failures and rebuild trust between docs and code.

## Existing strengths

- **Signed probe validation is well designed.** `validator.go` `parseProbeBody`
  verifies an HMAC over `nonce|ip`, rejects replayed or forged responses, and the
  nonce check plus `Cache-Control: no-store` close the obvious replay path.
  `probeClientIP` trusts `X-Real-IP` only from loopback, so a public client cannot
  forge its exit IP. **[code]**
- **SSRF and redirect hardening.** `sources.go` `HarvestSources` forces HTTPS,
  limits redirects to the originating publisher host or a curated allowlist, and
  `trustedFeedFallback` binds a jsDelivr fallback to the exact same repo, ref and
  path. `validEndpoint` rejects private, loopback and special-use ranges through a
  precompiled bogon-prefix table. **[code]**
- **Memory discipline under scale.** `store.go` streams compaction in 1,024-record
  chunks, `VisitRecords` walks under a read lock without allocating, the export
  path materialises only the Alive/Gold pool, and source fetching uses reservoir
  sampling so a million-line feed cannot exhaust RAM. The v1.5.3 changelog entry
  is corroborated by `memory_incident_test.go`. **[code]** **[run]**
- **Bounded, fair scheduling.** `source_schedule.go` `selectSourceBatch` reserves
  one quarter of the per-harvest budget for productive sources while rotating
  oldest-due exploration, and the worker loop in `engine.go` interleaves three
  urgent rechecks with one discovery item so live proxies are not starved.
  **[code]**
- **Honest quality reporting.** `quality.go` refuses to infer survival it did not
  observe, reports `learning`/`warming_up` states instead of inventing numbers,
  and the API says explicitly that AS registration country is not a physical
  proxy location. **[code]**
- **A working regression suite.** 52 tests cover candidate parsing, the store
  round-trip, compaction races, backoff, the v2 cursor/ETag contract, source
  fallback, and quality cohorts. `go test -race` passes. **[run]**
- **The static dashboard is self-contained and sanitised.** `site/index.html`
  issues no external requests, uses relative paths, and validates the commit SHA
  before placing it in a link. **[code]**

## Critical bugs or risks

None of these are data-loss or remote-code-execution issues at shipped
defaults, but each can cause a wrong result or a wedged deployment.

### C1. Survival cohorts credit proxies that were already alive before v1.5 — [code]

`quality.go` `updateQualityCohorts` sets `EpochStart = now` on the first
*signed success after deployment*, regardless of how long the proxy has been
alive. `docs/QUALITY_V15.md` and the README both state historic Alive records are
not retroactively assigned survival credit. They are: every existing Alive proxy
starts a cohort on its next recheck.

- **Why it matters:** the headline "survival rate" is inflated for the first
  weeks after each restart of the cohort epoch, which is exactly when operators
  look at it.
- **Next step:** record the record's `FirstSeen` and only start a cohort when
  `now - FirstSeen >= horizon` is still true at the first post-v1.5 success, or
  add a `CohortEpoch` field set once at upgrade and refuse to credit `FirstAt`
  earlier than it.

### C2. `https` proxies are validated as `http` first, and feed lines may lose their scheme — [code]

In `validator.go` `httpProxy`, an `https` record tries the `http` proxy scheme
first, then `https`. Separately, `sources.go` `parseCandidate` keeps the URL
scheme only if `normalizeProtocol(u.Scheme)` accepts it; a literal `https://`
line in a feed whose `protocol` is `http` is validated as an HTTP proxy. A proxy
that only speaks the `https` CONNECT scheme will fail first and may be demoted.

- **Why it matters:** classification errors quietly shrink the HTTPS pool and
  distort per-protocol stats.
- **Next step:** for `https` records attempt the `https` scheme first, or store
  the observed scheme alongside the declared one and validate against the
  observed scheme. Add a table test for each scheme/protocol combination.

### C3. Release smoke test asserts a stale feed count — [code]

`scripts/smoke-api-v2.py:20` asserts `source_feeds == 329`, but
`config/sources.json` contains 334 entries. The README calls this script the
release check.

- **Why it matters:** the documented release gate fails against the current
  config, so either the gate is being ignored or it is masking real failures.
- **Next step:** derive the expectation from `/api/sources` length, or assert
  only a minimum. Remove the hard-coded count from the README as well.

### C4. Registry-growth scripts abort or assert on hard-coded sizes — [code]

`promote-sources.py` asserts `70 <= len(publisher_count) <= 110` and
`len(selected) >= 160`; `admit-reviewed-feeds.py` asserts a batch of 5. Python
`-O` silently disables `assert`, so the guard also vanishes under optimisation.

- **Why it matters:** routine source curation fails as soon as the registry
  grows past the assumed size, and the failure mode is a stack trace rather than
  a clear refusal.
- **Next step:** replace bare `assert` with explicit `SystemExit` messages and
  compute expected sizes from the current registry.

### C5. IP-to-ASN refresh may never reach the engine — [runtime]

`config/pscaper-intel.service` runs under `ProtectSystem=full` and writes into
`/home/ubuntu/pscaper/data/intel`, while `intel.go` `Reload` only refreshes when
the file's `ModTime` changes and `update-ipintel.sh` does an atomic `mv` (new
inode, new mtime). If the sandbox still rejects the rename, `/api/asn` goes stale
and the daily timer fails silently.

- **Why it matters:** ASN/country filters silently return old data.
- **Next step:** add `ReadWritePaths=/home/ubuntu/pscaper/data/intel`, then
  `systemctl start pscaper-intel.service` and confirm mtime advances. This is the
  single highest-value item to check on the live host.

## Missing or incomplete functionality

### M1. Documented endpoints, flags and modes that differ from the docs — [code]

- `GET /api/stats`, `GET /api/top`, `GET /health`, and `POST /api/action/{harvest,export}`
  exist and are used by the TUI but are absent from the endpoint list.
- `pscaper serve|version|tui` — only `tui` is documented.
- `PSCAPER_PROBE_TOKEN` is optional (generated in memory when empty) but
  `.env.example` implies it is required.
- `README.md:577` and `QUALITY_V15.md:51` say the `/api/stats` aggregate cache
  is four seconds; `engine.go` `Stats()` caches for eight. (The four-second
  figure in `README.md:305` correctly describes `/api/live`.)
- `API_V2.md` omits `GET /api/v2/health` and the extra response fields.
- **Next step:** regenerate the endpoint and env-var reference from the route
  table and `config.go`, and add a `docs/ENDPOINTS.md` that the README links to.
  A short table test that walks `mux` would keep it honest.

### M2. Version numbers and feed counts are inconsistent across docs — [code]

`README.md` still says "1.4.2"; `CHANGELOG.md` is at 1.5.3 and `main.go` agrees;
`API_V2.md` says 1.4.2. Feed count is 329 in some places, 334 in others.
- **Next step:** make `main.go`'s `version` the single source of truth, inject it
  into the build with `-ldflags -X`, and have a doc check fail CI when the README
  version string drifts.

### M3. `install.sh` installs only 2 of ~12 systemd units — [code]

Only `pscaper.service` and the probe vhost are linked; the health watchdog,
intel, maintain, source-research, cert-renew, cloudflare-check and dashboard-sync
units are not. The watchdog's `ExecStart` path is not created by any script, and
`pscaper-memory-guard.conf` is never installed as a drop-in.
- **Why it matters:** a fresh install does not match the documented layout and
  the memory guard is inert.
- **Next step:** make `install.sh` enumerate and enable every unit, and fail if a
  referenced `ExecStart` script is missing.

### M4. No CI workflow in the repository — [code]

The suite runs and passes, but nothing runs it automatically.
- **Next step:** add a GitHub Actions job running `go vet ./...`, `go test -race ./...`,
  `gofmt -l`, and the doc-version check from M2 on every push.

## Performance and reliability improvements

### P1. Publication runs an SSH handshake and `git fetch` every 30 seconds — [code]

`scripts/publish-to-github.sh` authenticates and fetches before deciding there
is nothing to publish (~2,880 round-trips/day), with no backoff.
- **Why it matters:** avoidable GitHub rate-limit exposure and needless egress.
- **Next step:** trigger on change (`PathChanged=exports/`) or hash-compare
  `stats.json` before authenticating, and add exponential backoff on failure.

### P2. Publishing can wedge on a non-atomic swap — [code]

`scripts/maintain-publisher.sh` performs two separate `mv`s; a crash between
them leaves `repository.maintenance-old` behind, and the next run refuses to
start, halting publishing until an operator intervenes. Rollback scripts copy
the binary in place with `cp` rather than `install`/rename, so a crash mid-copy
leaves a truncated executable.
- **Next step:** write to `.new` and `rename()` in one step everywhere; on
  startup, detect and finish an interrupted swap instead of exiting.

### P3. Watchdog restarts without escalation — [code]

`pscaper-health-watch.sh` restarts the unit every cooldown with only
`logger` output. A process that is alive but hung will be restarted forever and
nobody is told.
- **Next step:** after N restarts in 24 h, stop and write an alarm that surfaces
  on the dashboard (`publisher/status.json` already has this shape).

### P4. Vectored counters and allocation hygiene — [code]

`Stats()` recomputes whole-pool aggregates on every uncached call; it is cached
8 s but every export also calls it. `newlyAlive`/`lostAlive` are monotonic
counters with no reset on restart, which is fine for a session view but easy to
misread as lifetime totals.
- **Next step:** document them as per-process counters in `/api/live`, and reuse
  the existing `ProxyIndex` snapshot when computing export counts.

### P5. Dead configuration — [code]

`PSCAPER_CYCLE_BACKOFF_MIN/MAX` are parsed and validated but never read, and
`waitForDrain` in `engine.go` has no callers.
- **Next step:** delete `waitForDrain`, and either adopt the backoff in
  `continuousLoop` or remove the config keys so operators do not tune a no-op.

## Security improvements

### S1. Manage-API action routes are unauthenticated by design — keep it that way, and test it — [code]

`POST /api/action/*` performs network work and a git push. It is safe only
because `PSCAPER_API_ADDR` is loopback and Nginx does not proxy it. If someone
later exposes the management port or adds a `/pool/` route that reaches a
mutable handler, the API becomes remote-triggerable.
- **Next step:** add a startup assertion that refuses to bind a non-loopback
  `PSCAPER_API_ADDR` unless an explicit override is set, and a test that
  enumerates public routes.

### S2. Setup script leaves a persistent personal credential on the host — [code]

`scripts/setup-github.sh` uses the operator's `gh` token via an HTTPS remote and
`gh auth setup-git`, which any process running as `ubuntu` can then use against
every repo on that account. This contradicts the deploy-key (SSH) model used
elsewhere.
- **Next step:** make `setup-github.sh` one-shot: create the repo via the API,
  switch to the deploy key, then `gh auth logout` and unset `credential.helper`.

### S3. `.env` is `source`d by several scripts — [code]

`doctor.sh`, `setup-github.sh` and the upgrade scripts `source .env`, so a value
containing `$(…)` executes. Tokens are random today, but hand-edited `.env` is a
latent shell-injection path.
- **Next step:** parse only `^[A-Z_]+=` lines and pass them as environment,
  never `source` the file.

### S4. Nginx `/pool/` has no path allowlist — [code], defence-in-depth

The Go handler allowlists exact filenames, but the Nginx `location ^~ /pool/`
forwards any path under that prefix to the API. It is one future route away from
a mistake.
- **Next step:** add `limit_except GET { deny all; }` and an explicit
  `location` per allowed file, plus `limit_req` for consistency with the API.

## Testing gaps

- **No tests for the validator wire formats.** `socks4`/`socks5` handshakes,
  the SOCKS5 ATYP branches and the HTTP-proxy `https` fallback have no direct
  tests. Add table tests with an in-process stub proxy. **[code]**
- **No tests for the public route table.** `StartAPIServer` (all v1/v2/live/
  event/pool handlers) has only indirect coverage. Add `httptest` cases for
  each route, status code, and filter-validation error. **[code]**
- **No test for `Stats()` under concurrent source mutation.** The race run is
  clean, but nothing exercises the combination. A single `-race` test that
  interleaves `Stats()` with `mergeSourceStatuses`/`creditNewVerifiedSource`
  would lock in the current locking and guard against a future refactor that
  quietly drops it. **[code]**
- **No tests for the operational scripts.** Python scripts use bare `assert`
  and fixed sizes (C4); a small `pytest` shim with fixture registries would
  catch regressions. **[code]**
- **No CI** (M4), so none of the above run automatically. **[code]**
- **No load or soak test.** Memory claims rest on one incident test; a 24-hour
  soak with a synthetic million-record store would validate the compaction and
  export paths over time. **[runtime]**

## Future features and enhancements

Ordered by value-to-effort, all additive:

1. **Dry-run mode for publishing** (`pscaper publish --dry-run`) that prints the
   file diff and exits, so the publish path can be tested without a remote.
2. **Per-protocol and per-ASN pool views** in the dashboard from the existing
   `/api/v2/proxies` filters; no new backend needed.
3. **Scheduled self-audit** that re-runs `smoke-api-v2.py`, `doctor.sh` and the
   doc-drift check weekly and writes a `status.json`-style report.
4. **Pluggable validator targets** (currently a single probe host) so an
   operator can validate against multiple destinations and detect geo- or
   destination-specific failures.
5. **Structured logs** (`log/slog` with a JSON handler) keyed by proxy and
   source, to make the 2-core VPS logs greppable without a shipping dependency.
6. **SQLite-backed store** behind the existing `Store` interface, replacing the
   journal+snapshot so compaction becomes unnecessary for larger pools.

## Prioritized roadmap (P0–P3)

Legend: **P0** is urgent and cheap; **P1** next release; **P2** follow-up;
**P3** backlog. Each item names the files and the concrete first step.

### P0 — Do first

| # | Item | Files | Why | First step |
|---|------|-------|-----|-----------|
| P0-1 | Verify IP-ASN refresh on the host (C5) | `config/pscaper-intel.service`, `scripts/update-ipintel.sh` | `[runtime]` may be silently stale | Add `ReadWritePaths`, run the service, confirm mtime advances |
| P0-2 | Fix smoke-test and registry-size asserts (C3, C4) | `scripts/smoke-api-v2.py`, `scripts/promote-sources.py`, `scripts/admit-reviewed-feeds.py` | Documented release gate is failing | Derive counts from config; replace `assert` with `SystemExit` |
| P0-3 | Make `Stats()`-under-mutation locking explicit and tested | `internal/app/engine.go` | Protect a correct design from regression | Add one `-race` interleaving test |
| P0-4 | Reconcile version and feed counts (M2) | `README.md`, `docs/API_V2.md`, `main.go` | Operators read wrong version | Single source of truth + doc check |

### P1 — Next release

| # | Item | Files | Why | First step |
|---|------|-------|-----|-----------|
| P1-1 | Correct cohort epoch semantics (C1) | `internal/app/quality.go`, `internal/app/store.go` | Inflated survival rate | Gate `EpochStart` on `FirstSeen` |
| P1-2 | Separate `http`/`https` probe attempt order (C2) | `internal/app/validator.go`, `internal/app/sources.go` | HTTPS misclassification | Try scheme first; add table tests |
| P1-3 | Publish on change with backoff (P1) | `scripts/publish-to-github.sh`, `config/pscaper-publish.*` | Rate-limit exposure | `PathChanged` or hash gate |
| P1-4 | Atomic publisher and binary swaps (P2) | `scripts/maintain-publisher.sh`, `scripts/rollback-*.sh` | Wedged publishing | `.new` + `rename()`, recover on start |
| P1-5 | Document the full route and env surface (M1) | `README.md`, `docs/`, `internal/app/server.go` | Hidden endpoints | Generate `docs/ENDPOINTS.md` |
| P1-6 | Add CI (M4) | `.github/workflows/` | Nothing runs the suite | vet, test -race, gofmt, doc check |

### P2 — Follow-up

| # | Item | Files | Why | First step |
|---|------|-------|-----|-----------|
| P2-1 | Install all units (M3) | `scripts/install.sh`, `config/` | Fresh installs diverge | Enumerate and enable |
| P2-2 | Loopback-only guard for the API (S1) | `internal/app/server.go`, `internal/app/config.go` | Prevent accidental exposure | Startup assertion + test |
| P2-3 | Watchdog escalation (P3) | `scripts/pscaper-health-watch.sh` | Silent restart loops | Promote to an alarm |
| P2-4 | Nginx `/pool/` allowlist (S4) | `config/pscaper-site-nginx.conf` | Defence in depth | `limit_except GET` |
| P2-5 | Setup-script credential hygiene (S2, S3) | `scripts/setup-github.sh`, `scripts/doctor.sh` | Persistent token on host | One-shot setup; parse, don't source |
| P2-6 | Remove dead config and code (P5) | `internal/app/engine.go`, `internal/app/config.go` | Operator confusion | Delete `waitForDrain`; decide on backoff |

### P3 — Backlog

| # | Item | Files | Why | First step |
|---|------|-------|-----|-----------|
| P3-1 | Validator wire-format tests | `internal/app/validator.go` | Untested protocols | Stub-proxy table tests |
| P3-2 | Route-table tests | `internal/app/server.go` | Hidden-regression surface | `httptest` per route |
| P3-3 | Script tests | `scripts/` | Bare asserts | `pytest` shim |
| P3-4 | Publishing dry-run | `cmd/pscaper`, `internal/app` | Test publish without a remote | `--dry-run` flag |
| P3-5 | Structured logging | `internal/app/` | 2-core VPS log hygiene | `log/slog` JSON |
| P3-6 | SQLite-backed store | `internal/app/store.go` | Removes compaction | Interface behind `Store` |
| P3-7 | Multi-target validation | `internal/app/validator.go` | Destination-specific failures | Configurable probe list |
| P3-8 | Soak test | `internal/app/memory_incident_test.go` | Validate long-run memory | 24 h synthetic run |

## Method and limitations

- Environment: Go 1.23.12 (downloaded into the gitignored `.toolchain/`,
  matching the path the `Makefile` already prefers). `go vet`, `go build`,
  `go test ./...` and `go test -race ./...` all pass on `3e033ef`.
- A temporary race probe was created and then removed to confirm the
  `Stats()`/source-mutation locking; the race detector itself was sanity-checked
  with a known-racy test. No production code was modified.
- Findings marked **[runtime]** depend on the live VPS, systemd version or
  out-of-tree backups and were not confirmed here. The `recovery/` backup tree,
  `data/tls/`, and `/var/www/pscaper/index.html` are gitignored and could not be
  inspected.
- Performance figures quoted in the README were not re-measured; the audit
  assesses design and code, not live throughput.
