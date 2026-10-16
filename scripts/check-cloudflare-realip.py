#!/usr/bin/env python3
"""Read-only drift check for official Cloudflare real-IP CIDRs.

Nginx must only trust IP headers from Cloudflare's real published edge ranges.
This check never overwrites config or reloads the reverse proxy.
"""
import ipaddress
import pathlib
import re
import urllib.request

ROOT=pathlib.Path(__file__).resolve().parents[1]
EXPECTED=ROOT/'config/cloudflare-realip.conf'
def cidrs(lines):
 result=set()
 for line in lines:
  token=line.strip()
  if not token or token.startswith('#'):continue
  ip=ipaddress.ip_network(token,strict=True)
  if not ip.is_global:raise ValueError(f'unexpected non-public CIDR: {ip}')
  result.add(str(ip))
 return result

def remote(path):
 url='https://www.cloudflare.com/'+path
 request=urllib.request.Request(url,headers={'User-Agent':'pscaper-realip-health-check/1.4'})
 with urllib.request.urlopen(request,timeout=14) as response:
  return cidrs(response.read(12000).decode().splitlines())
def main():
 text=EXPECTED.read_text()
 local=cidrs(re.findall(r'^set_real_ip_from\s+([^;]+);',text,re.M))
 if len(local)<19:raise SystemExit('Configured ranges unexpectedly sparse')
 official=remote('ips-v4')|remote('ips-v6')
 if len(official)<19:raise SystemExit('Official ranges unexpectedly sparse')
 missing=sorted(official-local)
 obsolete=sorted(local-official)
 if missing or obsolete:
  print('Cloudflare IP RANGE CHANGE DETECTED; review config before manually changing Nginx.')
  print('Newly announced ranges:',missing)
  print('No longer listed:',obsolete)
  raise SystemExit(2)
 print(f'Cloudflare real-IP list verified: {len(local)} official public CIDRs, unchanged.')
if __name__=='__main__':main()
