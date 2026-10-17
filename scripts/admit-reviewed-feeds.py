#!/usr/bin/env python3
"""Conservatively promote a reviewed batch of distinct plaintext public feeds.

A feed must have a recent successful bounded network audit, at least ten
public-IP candidates, and a genuinely different content/overlap fingerprint.
This does not treat alternate CDN URLs as independent feeds.
"""
import argparse,datetime,json,pathlib,re,collections,urllib.parse
ROOT=pathlib.Path(__file__).resolve().parents[1]
REGISTRY=ROOT/'config/sources.json'
AUDIT=ROOT/'recovery/feed-scale-20261017/candidates-audited.json'
BATCH=[
 ('watchttvv/free-proxy-list','proxy.txt'),
 ('gnxD3RfTT2WE/socks-proxy-list-free','http.txt'),
 ('gnxD3RfTT2WE/free-proxy-list-daily','http.txt'),
 ('Denisyoya/Proxy-List-Gamt','results/raw/txt/socks5.txt'),
 ('nguywnben/daily-proxy-updates','proxies/socks5.txt'),
]
def main():
 arg=argparse.ArgumentParser()
 arg.add_argument('--apply',action='store_true')
 opts=arg.parse_args()
 from time import time
 report=json.loads(AUDIT.read_text())
 if time()-report['timestamp']>7200:raise SystemExit('Audit is stale. Rerun bounded checks.')
 lookup={(x['repo'],x['path']):x for x in report['items']}
 old=json.loads(REGISTRY.read_text())
 urls={x['url'].lower() for x in old}
 names={x['name'] for x in old}
 new=[]
 chosen=[]
 hashes=set()
 for key in BATCH:
  r=lookup.get(key)
  if not r or not r['eligible'] or r['unique_sample']<10 or r['error']:
   raise SystemExit('Candidate did not pass audit: '+str(key))
  if r['url'].lower() in urls or r['sha256'] in hashes:
   raise SystemExit('Duplicate URL or content in requested batch: '+str(key))
  sample=set(r['overlap_sample'])
  for other in chosen:
   prior=set(other['overlap_sample'])
   if len(sample&prior)/max(1,min(len(sample),len(prior)))>.85:
    raise SystemExit('Content overlap too high: '+str(key))
  uri=urllib.parse.urlsplit(r['url'])
  if uri.scheme!='https' or uri.hostname!='raw.githubusercontent.com':
   raise SystemExit('Nontrusted source URI')
  owner,repo=key[0].split('/')
  slug=lambda t:re.sub('[^a-z0-9]+','-',t.lower()).strip('-')
  name='exp-'+slug(owner)+'-'+slug(repo)[:26]+'-'+r['protocol']
  if name in names:raise SystemExit('Duplicate source name: '+name)
  new.append({'name':name,'url':r['url'],'protocol':r['protocol'],
              'refresh_seconds':3600,'max_candidates':75,'max_bytes_mb':8})
  chosen.append(r)
  urls.add(r['url'].lower());names.add(name);hashes.add(r['sha256'])
 print('Existing:',len(old),'Audited new:',len(new),'Future total:',len(old)+len(new))
 for r,entry in zip(chosen,new):
  print(entry['name'],'valid_sample',r['unique_sample'],'url',entry['url'])
 if not opts.apply:return
 path=ROOT/'recovery/feed-scale-20261017/selected-new-feeds.json'
 path.write_text(json.dumps({'admitted_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),
  'count':len(new),'new':new},indent=2)+'\n')
 tmp=REGISTRY.with_suffix('.json.tmp')
 tmp.write_text(json.dumps(old+new,indent=2)+'\n')
 tmp.replace(REGISTRY)
 print('Applied',len(new),'new sources, total',len(old)+len(new))
if __name__=='__main__':main()
