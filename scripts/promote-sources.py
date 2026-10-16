#!/usr/bin/env python3
"""Conservatively promote audited public proxy feeds into pscaper.

Reads the *sample-audit report*, not unchecked search results. Never inserts
the source if it is already configured, a publisher already used, or a sample
is a content clone in the same publisher+protocol. Write backups before edits.
This is a manual tool; the service reads config/sources.json at harvest time.
"""
import argparse
import collections
import datetime
import ipaddress
import json
import pathlib
import re
import shutil
import urllib.parse

ROOT=pathlib.Path(__file__).resolve().parents[1]
BASE=ROOT/'config/sources.json'
REPORT=ROOT/'recovery/research-sources-report.json'
def publisher(url):
 u=urllib.parse.urlparse(url)
 if u.hostname=='raw.githubusercontent.com':return '/'.join(u.path.strip('/').split('/')[:2]).lower()
 if u.hostname=='cdn.jsdelivr.net' and '/gh/' in u.path:return u.path.split('/gh/')[1].split('@')[0].lower()
 return u.hostname or ''
def trusted_url(url):
 u=urllib.parse.urlparse(url)
 host=u.hostname or ''
 if u.scheme!='https' or u.username or u.password or u.port:return False
 return host in {'raw.githubusercontent.com','cdn.jsdelivr.net','api.proxyscrape.com','openproxylist.xyz','proxyspace.pro','databay.com'} or host.endswith('.github.io')
def main():
 ap=argparse.ArgumentParser()
 ap.add_argument('--apply',action='store_true')
 ap.add_argument('--max-per-publisher',type=int,default=4)
 ap.add_argument('--min-valid',type=int,default=6)
 args=ap.parse_args()
 current=json.loads(BASE.read_text())
 recent=json.loads(REPORT.read_text())['candidates_data']
 used_urls={u['url'].lower() for u in current}
 old_publishers={publisher(u['url']) for u in current}
 publisher_count=collections.Counter()
 per_type=collections.Counter()
 selected=[]
 fingerprints=collections.defaultdict(list)
 items=sorted(recent,key=lambda x:(-len(x.get('curators',[])),-int(x['valid']),x['url']))
 for src in items:
  url=src['url'];proto=src['protocol']
  group=publisher(url)
  if (src['valid']<args.min_valid or not trusted_url(url) or url.lower() in used_urls
      or group in old_publishers or publisher_count[group]>=args.max_per_publisher
      or per_type[(group,proto)]>=2):continue
  sample=set(src.get('sample_keys',[]))
  # identical samples from the same publisher+protocol are a strong mirror signal
  if sample and any(len(sample&prev)/max(1,len(sample|prev))>.85 for prev in fingerprints[(group,proto)]):
   continue
  # Stable names, avoid exposing untrusted HTML/URLs in names.
  raw=re.sub('[^a-z0-9]+','-',group.lower()).strip('-')[:34]
  n=per_type[(group,proto)]+1
  name=f'discovery-{raw}-{proto}-{n}'
  if name in (u['name'] for u in current):continue
  selected.append({'name':name,'url':url,'protocol':proto,'refresh_seconds':7200,
     'max_candidates':250,'max_bytes_mb':8})
  used_urls.add(url.lower())
  publisher_count[group]+=1
  per_type[(group,proto)]+=1
  fingerprints[(group,proto)].append(sample)
 print('Existing:',len(current),'new:',len(selected),'new independent publisher keys:',len(publisher_count))
 print('New feed protocol counts:',dict(collections.Counter(x['protocol'] for x in selected)))
 print('Maximum *admitted* new candidates per complete refresh:',sum(x['max_candidates'] for x in selected))
 if not args.apply:return
 assert 70<=len(publisher_count)<=110,'unexpected publisher count'
 assert len(selected)>=160,'unexpectedly weak source audit results'
 path=ROOT/'recovery/source-backups'
 path.mkdir(exist_ok=True)
 ts=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
 backup=path/f'sources-before-extended-{ts}.json'
 shutil.copy2(BASE,backup)
 combined=current+selected
 assert len({x['name'] for x in combined})==len(combined)
 assert len({x['url'].lower() for x in combined})==len(combined)
 temp=BASE.with_suffix('.json.next')
 temp.write_text(json.dumps(combined,indent=2)+'\n')
 temp.replace(BASE)
 note=ROOT/'recovery/source-promotion.json'
 note.write_text(json.dumps({'generated_at':ts,'previous':len(current),'new':len(selected),
    'total':len(combined),'new_publishers':len(publisher_count),
    'backup':str(backup),'selected':selected},indent=2)+'\n')
 print('Promoted source count:',len(combined),'backup:',backup)
if __name__=='__main__':main()
