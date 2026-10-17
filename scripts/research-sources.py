#!/usr/bin/env python3
"""One-time, bounded discovery audit of public plaintext proxy publishers.

Collects curated source index URLs for human-reviewed promotion into config.
Never tests candidates as proxies; pscaper's validator remains authoritative.
"""
import concurrent.futures
import collections
import ipaddress
import json
import pathlib
import re
import time
import urllib.parse
import urllib.error
import urllib.request

ROOT=pathlib.Path(__file__).resolve().parents[1]
SAVED=ROOT/'recovery'
SRC=ROOT/'config/sources.json'
links=[
 ('maximilian', 'https://raw.githubusercontent.com/maximilianfeix/proxy-scraper/main/proxyscraper/sources.json'),
 ('gfp-http','https://raw.githubusercontent.com/gfpcom/free-proxy-list/main/sources/http.txt'),
 ('gfp-https','https://raw.githubusercontent.com/gfpcom/free-proxy-list/main/sources/https.txt'),
 ('gfp-socks4','https://raw.githubusercontent.com/gfpcom/free-proxy-list/main/sources/socks4.txt'),
 ('gfp-socks5','https://raw.githubusercontent.com/gfpcom/free-proxy-list/main/sources/socks5.txt'),
 ('fmw-http','https://raw.githubusercontent.com/fmw86/free-proxy-pool/main/sources_http.txt'),
 ('fmw-socks4','https://raw.githubusercontent.com/fmw86/free-proxy-pool/main/sources_socks4.txt'),
 ('fmw-socks5','https://raw.githubusercontent.com/fmw86/free-proxy-pool/main/sources_socks5.txt'),
]
headers={'User-Agent':'pscaper-source-audit/2.0','Accept':'text/plain,application/json;q=0.9'}
ALLOWED_HOSTS={'raw.githubusercontent.com','cdn.jsdelivr.net','api.proxyscrape.com','openproxylist.xyz','proxyspace.pro','databay.com'}
def safe_public_feed(url):
 try:
  u=urllib.parse.urlparse(url)
  if u.scheme!='https' or not u.hostname or u.username or u.password:return False
  if u.port not in (None,443):return False
  return u.hostname in ALLOWED_HOSTS or u.hostname.endswith('.github.io')
 except (TypeError,ValueError):return False
class RestrictedRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,req,fp,code,msg,headers,newurl):
  if not safe_public_feed(newurl):
   raise urllib.error.HTTPError(req.full_url,code,'untrusted redirect destination',headers,fp)
  return super().redirect_request(req,fp,code,msg,headers,newurl)
opener=urllib.request.build_opener(RestrictedRedirect())

def get(url,timeout=12):
 req=urllib.request.Request(url,headers=headers)
 with opener.open(req,timeout=timeout) as res:
  code=res.status
  return code,res.read(256*1024)
base=json.loads(SRC.read_text())
existing={(x['protocol'],x['url'].lower()) for x in base}
store={}
for tag,url in links:
 try:
  _,buf=get(url)
  if tag=='maximilian':
   obj=json.loads(buf)
   rows=[(proto if proto!='auto' else 'http',q) for proto,ls in obj['sources'].items() for q in ls]
  else:
   proto='http' if tag in ('gfp-http','gfp-https') else tag[4:]
   rows=[(proto,x.strip()) for x in buf.decode('utf-8','replace').splitlines()]
  for protocol,raw in rows:
   if not isinstance(raw,str):continue
   u=urllib.parse.urlparse(raw.strip())
   if not safe_public_feed(raw.strip()):continue
   if '/wiki/' in u.path or u.path.endswith(('.csv','.json','.md','.toml','.yaml','.yml')):continue
   key=(protocol,u.geturl().lower())
   if key in existing:continue
   entry=store.setdefault(key,{'url':raw.strip(),'protocol':protocol,'curators':[]})
   if tag not in entry['curators']:entry['curators'].append(tag)
  print('index',tag,len(rows),'retained total',len(store),flush=True)
 except Exception as ex:print('index failed',tag,str(ex)[:200],flush=True)
pattern=re.compile(r'(?i)^\s*(?:(https?|socks[45])://)?(\d{1,3}(?:\.\d{1,3}){3}):(\d{1,5})(?::[^\r\n]*)?\s*$')
def audit(data):
 st=time.monotonic()
 row={**data,'status':0,'valid':0,'sample_lines':0,'duration_ms':0,'sample_keys':[],'error':''}
 try:
  request=urllib.request.Request(row['url'],headers=headers)
  with opener.open(request,timeout=12) as res:
   row['status']=res.status
   if 'application/json' in res.headers.get('Content-Type',''):raise ValueError('JSON response unsupported')
   buf=res.read(128*1024)
  text=buf.decode('utf-8','replace')
  if len(buf)>=128*1024:text=text.rsplit('\n',1)[0]
  lines=text.splitlines()
  row['sample_lines']=len(lines)
  valid=set()
  for line in lines:
   mat=pattern.match(line)
   if not mat:continue
   p=mat.group(1)
   if p and p!=row['protocol']:continue
   ip=ipaddress.ip_address(mat.group(2))
   port=int(mat.group(3))
   if ip.is_global and 0<port<65536:valid.add((ip.compressed,port))
  row['valid']=len(valid)
  row['sample_keys']=[f'{ip}:{port}' for ip,port in sorted(valid)[:45]]
  if len(valid)<6:row['error']='insufficient valid endpoint sample'
 except Exception as e:row['error']=str(e)[:130]
 row['duration_ms']=round((time.monotonic()-st)*1000)
 return row
print('AUDITING',len(store),'unseen unique URLs with max 12 concurrency',flush=True)
rows=list(store.values())
results=[]
with concurrent.futures.ThreadPoolExecutor(max_workers=12) as executor:
 for r in executor.map(audit,rows):
  results.append(r)
  if len(results)%60==0:print('PROGRESS',len(results),'/',len(rows),'valid',sum(x['valid']>=6 for x in results),flush=True)
good=[r for r in results if r['valid']>=6]
tmp=SAVED/'research-sources-report.json.next'
tmp.write_text(json.dumps({'generated_at':time.time(),'candidates':len(results),'working':len(good),'candidates_data':results},indent=2)+'\n')
tmp.replace(SAVED/'research-sources-report.json')
print('COMPLETE',len(results),'valid',len(good),'http-status',dict(collections.Counter(r['status'] for r in results)),flush=True)
