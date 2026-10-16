#!/usr/bin/env bash
# Licensed public-domain IPtoASN data, cached entirely inside pscaper.
set -euo pipefail
ROOT=/home/ubuntu/pscaper
DIR="$ROOT/data/intel"
mkdir -p "$DIR"
FILE="$DIR/ip2asn-v4-u32.tsv.gz"
NEXT="$FILE.next"
trap 'rm -f -- "$NEXT"' EXIT
curl -fSL --retry 2 --connect-timeout 10 --max-time 120 --silent --show-error \
 'https://iptoasn.com/data/ip2asn-v4-u32.tsv.gz' -o "$NEXT"
gzip -t "$NEXT"
[[ "$(wc -c < "$NEXT")" -gt 500000 ]] || { echo 'ASN dataset unexpectedly small' >&2; exit 1; }
mv -f "$NEXT" "$FILE"
echo "ASN dataset refreshed: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
