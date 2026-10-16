#!/usr/bin/env python3
"""Read-only end-to-end pscaper API compatibility and isolation smoke test."""
import json
import urllib.error
import urllib.parse
import urllib.request

BASE='http://127.0.0.1:8788'
def get(path,headers=None):
 req=urllib.request.Request(BASE+path,headers=headers or {})
 try:
  with urllib.request.urlopen(req,timeout=12) as res:
   return res.status,res.headers,json.load(res)
 except urllib.error.HTTPError as err:
  return err.code,err.headers,err.read().decode()[:150]

def main():
 status,_,live=get('/api/live')
 assert status==200
 assert live['source_feeds']==329 and live['alive']>0
 assert live.get('publishing',{}).get('state')=='ok'
 print('Live: PASS,',live['alive'],'Alive,',live['gold'],'Gold,',live['source_feeds'],'feeds')

 status,head,a=get('/api/v2/proxies?limit=3')
 assert status==200 and a['count']==3 and a['total']>=3
 urls=[p['url'] for p in a['proxies']]
 assert all('score' in p and 'recent_success_pct' in p and 'last_checked' in p for p in a['proxies'])
 token=a['next_cursor']
 assert token
 status,_,b=get('/api/v2/proxies?limit=3&cursor='+urllib.parse.quote(token,safe=''))
 assert status==200 and b['count']==3 and set(urls).isdisjoint(p['url'] for p in b['proxies'])
 print('V2 strict cursors and reliability fields: PASS')

 status,_,v1=get('/api/v1/proxies?limit=3')
 assert status==200 and urls==[p['url'] for p in v1['proxies']]
 print('V1 compatibility and sorting: PASS')

 status,_,_=get('/api/v2/proxies?limit=999')
 assert status==400
 status,_,_=get('/api/v2/proxies?limit=3&min_score=NaN')
 assert status==400
 print('Malformed filter handling: PASS')

 etag=head.get('ETag')
 assert etag
 status,_,_=get('/api/v2/proxies?limit=3',{'If-None-Match':etag})
 assert status==304
 print('ETag/304: PASS')

 status,_,gold=get('/api/v2/proxies?tier=gold&limit=3')
 assert status==200 and gold['total']<=live['gold']+25
 print('Gold pool filter: PASS')

 status,_,insight=get('/api/v2/intelligence')
 assert status==200 and insight['status'] in ('warming_up','ok')
 status,_,health=get('/api/v2/health')
 assert status==200 and health['service']=='pscaper'
 print('Health and Intelligence: PASS')

 # Public Nginx allowlist: no state-changing management route is accessible.
 req=urllib.request.Request('https://proxy.syndrid.com/api/action/export',
     method='POST',data=b'',headers={'User-Agent':'pscaper-release-smoke/1.4'})
 try:
  urllib.request.urlopen(req,timeout=12)
  raise AssertionError('Private management action unexpectedly exposed')
 except urllib.error.HTTPError as err:
  assert err.code in (403,404,405),f'unexpected admin response {err.code}'
 print('Management API isolation: PASS')
 print('All smoke checks passed.')
if __name__=='__main__':main()
