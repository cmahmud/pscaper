#!/usr/bin/env python3
"""Read-only pscaper operational trend report from persisted telemetry.

Usage:
  python3 scripts/health-compare.py --hours 24
No network requests, changing services, or saved IP/proxy addresses.
"""
import argparse
import datetime as dt
import json
import pathlib
import statistics

ROOT=pathlib.Path(__file__).resolve().parents[1]
def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--hours',type=int,default=24)
    parser.add_argument('--json',action='store_true')
    args=parser.parse_args()
    if args.hours<1 or args.hours>720:parser.error('hours must be 1–720')
    now=dt.datetime.now(dt.timezone.utc)
    cutoff=now-dt.timedelta(hours=args.hours)
    rows={}
    for line in (ROOT/'data/telemetry/history.jsonl').open():
        try:
            p=json.loads(line)
            t=dt.datetime.fromisoformat(p['time'].replace('Z','+00:00'))
            if cutoff<=t<=now:rows[t]=p
        except (KeyError,ValueError,TypeError,json.JSONDecodeError):
            continue
    ordered=sorted(rows.items())
    span=(ordered[-1][0]-ordered[0][0]).total_seconds()/3600 if len(ordered)>1 else 0
    gaps=[(ordered[i][0]-ordered[i-1][0]).total_seconds()/60 for i in range(1,len(ordered))]
    result={'requested_hours':args.hours,'coverage_hours':round(span,2),
        'is_full_window':span>=args.hours-0.5,'observations':len(ordered),
        'first':ordered[0][0].isoformat() if ordered else None,
        'last':ordered[-1][0].isoformat() if ordered else None,
        'max_gap_minutes':round(max(gaps),1) if gaps else None,
        'averaging':'unweighted available telemetry sample average'}
    for key in ('alive','gold'):
        values=[p[key] for _,p in ordered if isinstance(p.get(key),(int,float))]
        result[key]={'count':len(values),'min':min(values),'mean':round(statistics.mean(values),1),
                     'max':max(values)} if values else None
    if args.json:
        print(json.dumps(result,indent=2))
    else:
        print('pscaper %dh trend: %d samples, %.2f hours observed, complete=%s' %(
              args.hours,len(ordered),span,result['is_full_window']))
        for key in ('alive','gold'):
            print(key,result[key])
        print('Longest telemetry gap (min):',result['max_gap_minutes'])
        if not result['is_full_window']:
            print('PARTIAL DATA: Do not interpret as a complete 24-hour result.')
if __name__=='__main__':
    main()
