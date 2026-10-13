#!/usr/bin/env python3
"""Bounded, read-only feed health audit (samples; never trusts upstream labels)."""
import concurrent.futures
import ipaddress
import json
import pathlib
import re
import sys
import time
import urllib.error
import urllib.request
from collections import Counter

BASE = pathlib.Path(__file__).resolve().parents[1]
DEFAULT = BASE / "config" / "sources.json"
RESULT = BASE / "recovery" / "feed-audit.json"
LIMIT = 128 * 1024
PATTERN = re.compile(r"^\s*(?:(?:https?|socks[45])://)?(\d{1,3}(?:\.\d{1,3}){3}):(\d{1,5})(?::[^\r\n]*)?\s*$", re.I)


def audit(source):
    start = time.monotonic()
    req = urllib.request.Request(source["url"], headers={"User-Agent": "pscaper-feed-audit/1.0", "Accept": "text/plain"})
    result = {"name": source["name"], "url": source["url"], "protocol": source["protocol"], "http_status": 0,
              "sample_bytes": 0, "sample_valid": 0, "sample_lines": 0, "error": ""}
    try:
        with urllib.request.urlopen(req, timeout=12) as resp:
            result["http_status"] = resp.status
            payload = resp.read(LIMIT)
        result["sample_bytes"] = len(payload)
        text = payload.decode("utf-8", "replace")
        if len(payload) == LIMIT:
            text = text.rsplit("\n", 1)[0] # avoid partial trailing record
        lines = text.splitlines()
        result["sample_lines"] = len(lines)
        seen = set()
        for line in lines:
            m = PATTERN.fullmatch(line)
            if m:
                try:
                    ip = ipaddress.ip_address(m.group(1))
                    port = int(m.group(2))
                    if ip.is_global and 0 < port < 65536:
                        seen.add((ip.compressed, port))
                except ValueError:
                    pass
        result["sample_valid"] = len(seen)
        if not seen:
            result["error"] = "No valid IPv4:port in bounded sample"
    except Exception as exc:
        result["error"] = f"{type(exc).__name__}: {str(exc)[:140]}"
    result["duration_ms"] = round((time.monotonic() - start) * 1000)
    return result


def main():
    path = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else DEFAULT
    sources = json.loads(path.read_text())
    with concurrent.futures.ThreadPoolExecutor(max_workers=12) as exe:
        results = list(exe.map(audit, sources))
    results.sort(key=lambda x: x["name"])
    good = [x for x in results if x["sample_valid"] > 0]
    bad = [x for x in results if x["sample_valid"] == 0]
    print(f"TOTAL={len(results)} OK={len(good)} FAILED={len(bad)}")
    print("RESPONSES:", dict(Counter(x["http_status"] for x in results)))
    print("Top 14 samples:")
    for x in sorted(good, key=lambda x: -x["sample_valid"])[:14]:
        print(f"  {x['name']} {x['sample_valid']} samples in {x['sample_bytes']} bytes, {x['duration_ms']} ms")
    print("UNHEALTHY:")
    for x in bad:
        print(f"  {x['name']}: http={x['http_status']} {x['error']}")
    RESULT.parent.mkdir(parents=True, exist_ok=True)
    RESULT.write_text(json.dumps({"sources": results, "ok": len(good), "failed": len(bad)}, indent=2) + "\n")
    print("Report:", RESULT)


if __name__ == "__main__":
    main()
