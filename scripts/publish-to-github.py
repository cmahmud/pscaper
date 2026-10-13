#!/usr/bin/env python3
"""Atomically update an isolated Git checkout from locally exported public lists.

Never replace historical lists with smaller subsets; retain original Git history.
Never access .env, app source, certificates, or application control endpoints.
"""
import json
import pathlib
import re
import sys

SRC = pathlib.Path(sys.argv[1]).resolve()
DST = pathlib.Path(sys.argv[2]).resolve()
assert SRC == pathlib.Path("/home/ubuntu/pscaper/exports")
assert DST == pathlib.Path("/home/ubuntu/pscaper/data/publisher/repository")
protocols = ["all", "http", "https", "socks4", "socks5"]
paths = [f"{tier}/{proto}.txt" for tier in ("alive", "gold") for proto in protocols]
paths += [f"historical/{name}.txt" for name in ("all-discovered", "ever-alive", "ever-gold")]
paths += ["stats.json"]

stats = json.loads((SRC / "stats.json").read_text())
assert stats.get("total", 0) > 100000, "Snapshot looks incomplete"
assert stats.get("alive", -1) >= 0, "Missing active proxy count"
for rel in paths:
    inp = SRC / rel
    out = DST / rel
    body = inp.read_bytes()
    if rel.startswith("historical/"):
        old = out.read_text().splitlines() if out.exists() else []
        latest = body.decode("utf-8").splitlines()
        merged = sorted(set(old) | set(latest))
        body = ("\n".join(merged) + "\n").encode("utf-8")
    if rel.endswith(".txt") and body:
        head = body.splitlines()[0].decode("utf-8")
        if not re.fullmatch(r"(?:(?:https?|socks[45])://)?[\[\]0-9a-fA-F.:]+:[0-9]{1,5}", head):
            raise RuntimeError("Invalid proxy data in "+rel)
    out.parent.mkdir(parents=True, exist_ok=True)
    if not out.exists() or out.read_bytes() != body:
        out.write_bytes(body)
values = {k: int(stats.get(k, 0)) for k in ("alive", "gold", "total", "ever_alive", "ever_gold")}
readme = f"""# pscaper — validated public proxy pool

Automated read-only proxy exports from **[pscaper](https://github.com/cmahmud/pscaper)**.
Live dashboard: **[proxy.syndrid.com](https://proxy.syndrid.com/)**.

## Latest published counters

| Metric | Count |
| --- | ---: |
| Alive now | {values["alive"]:,} |
| Gold now | {values["gold"]:,} |
| Unique discovered | {values["total"]:,} |
| Ever alive | {values["ever_alive"]:,} |
| Ever Gold | {values["ever_gold"]:,} |

## Data

- [alive/](alive/) — validated proxies
- [gold/](gold/) — repeated validated proxies
- [historical/](historical/) — cumulative proxy history
- [stats.json](stats.json) — full snapshot
- [Automation](.github/workflows/pscaper-sync.yml) — GitHub Actions fallback

These are time-sensitive lists of untrusted public proxies. Do not send
credentials or sensitive data through them.
"""
(DST / "README.md").write_text(readme)
print("Ready:",len(paths),"public exports",values)
