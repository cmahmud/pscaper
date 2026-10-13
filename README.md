# pscaper 1.3

pscaper is a high-concurrency public-proxy feed harvester, validator, scorer, exporter, and terminal dashboard. It consumes explicitly configured public proxy-list feeds; it does **not** scan arbitrary Internet address ranges for open proxy ports.

## v1.3: wide-source harvester

v1.3 expands the bundled configuration to **106 protocol feeds from 31 independent public-list publishers/origins**. The engine still deduplicates by `protocol://IP:PORT`, so pulling the same proxy from ten publishers does not inflate `DISCOVERED`.

The source catalog includes fast-refresh feeds, hourly feeds, daily feeds, and several very large aggregate feeds. Every configured proxy is independently checked through pscaper's own probe before it can enter `alive/` or `gold/`.

### Per-source scheduling

Each source now carries its own `refresh_seconds` value. Continuous mode no longer re-downloads all 106 endpoints every few seconds.

Typical flow:

```text
source updated every 5m  -> fetch when its 5m cadence is due
source updated every 30m -> fetch when its 30m cadence is due
hourly source            -> fetch hourly
daily source             -> fetch daily

any due source(s)
  -> ingest + dedupe
  -> validate NEW/DUE candidates
  -> export current + historical pools
  -> push changed pools to private GitHub
  -> continue forever
```

`H` in the TUI is an explicit **force-all-sources** action and bypasses those cadences.

### Large-feed support

`Source` entries can set `max_bytes_mb`. Normal feeds default to 16 MiB; the huge aggregate feeds bundled in v1.3 are allowed up to 64 MiB. The fresh-install defaults are also raised to:

```text
PSCAPER_SOURCE_CONCURRENCY=24
PSCAPER_QUEUE_SIZE=3000000
PSCAPER_WORKERS=3000
```

The v1.3 upgrade changes the existing 12-source-concurrency / 200000-queue defaults only when those exact old defaults are still present. Custom values are left alone.

## TUI additions

The dashboard now includes source scheduling telemetry:

```text
SOURCE FEEDS     106   FETCHED/HRV       17   DUE    0   ERR    1
```

Each displayed source also shows when it becomes due again. This makes it clear that the endless engine is still active even when no publisher is currently due for another download.

Main counters remain:

```text
DISCOVERED    all unique protocol://IP:PORT records known
ALIVE NOW     currently passing validation
GOLD NOW      current repeatedly verified/high-score subset
EVER ALIVE    succeeded at least once
EVER GOLD     qualified as Gold at least once
DEAD NOW      currently failing
```

## Private GitHub export

The v1.2 history layout is preserved:

```text
alive/                         current passing pool
gold/                          current Gold pool
historical/
  all-discovered.txt           every unique record ever discovered
  ever-alive.txt               every record that ever passed
  ever-gold.txt                every record that ever became Gold
archive/YYYY-MM-DD/            periodic current-pool snapshots
stats.json
README.md
```

Existing history, Git credentials, repo configuration, proxy state, and probe settings are preserved by the v1.3 upgrade.

## TUI

```bash
set -a
source .env
set +a
./bin/pscaper tui
```

Keys:

```text
H     force all configured sources now
E/P   export + private-GitHub push now
Q     close the TUI (the systemd engine keeps running)
```

## Important first-run behavior

The first v1.3 service start treats every configured publisher as due. Because the new catalog includes very large aggregate lists, `DISCOVERED` and `QUEUED` can jump sharply and the first validation cycle can take much longer than v1.2. That is intentional. Subsequent downloads follow each source's own refresh cadence.

## Security

Public proxies are untrusted. Do not route passwords, session cookies, API keys, banking traffic, or other secrets through them. pscaper does not include credential interception, TLS MITM, Internet-wide open-proxy scanning, or anti-abuse bypass logic.

## GitHub export visibility and attribution

`setup-github.sh` supports either a private or public export repository. Set `PSCAPER_GITHUB_VISIBILITY=public` when the generated pool is intentionally public. Future export commits are authored with the authenticated GitHub account's ID-based `noreply` address by default, so GitHub can associate qualifying commits with that account without exposing a personal email address. Override `PSCAPER_GIT_AUTHOR_NAME` or `PSCAPER_GIT_AUTHOR_EMAIL` only when needed.

The management API remains bound to `127.0.0.1` by default. A public website should consume the exported files or a separate read-only publication layer rather than expose the management API.
